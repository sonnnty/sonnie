package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/logring"
)

type fakeMembers struct {
	roles map[string][]string // "guild/user" → roles; missing = 404
	err   error
}

func (f fakeMembers) GuildMember(guildID, userID string, _ ...discordgo.RequestOption) (*discordgo.Member, error) {
	if f.err != nil {
		return nil, f.err
	}
	roles, ok := f.roles[guildID+"/"+userID]
	if !ok {
		return nil, &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusNotFound}}
	}
	return &discordgo.Member{Roles: roles, User: &discordgo.User{ID: userID}}, nil
}

func testSession(t *testing.T) *discordgo.Session {
	t.Helper()
	st := discordgo.NewState()
	st.User = &discordgo.User{ID: "bot", Username: "SONNIE"}
	if err := st.GuildAdd(&discordgo.Guild{
		ID: "g1", Name: "บ้านเพื่อน", OwnerID: "owner", MemberCount: 7,
		Roles: []*discordgo.Role{
			{ID: "g1", Permissions: 1 << 10},
			{ID: "mod", Permissions: discordgo.PermissionManageGuild},
			{ID: "plain", Permissions: 1 << 11},
		},
		Channels: []*discordgo.Channel{{ID: "c1", GuildID: "g1", Name: "แชท"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.MemberAdd(&discordgo.Member{GuildID: "g1", Nick: "ชาย", User: &discordgo.User{ID: "u1", Username: "somchai"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.MemberAdd(&discordgo.Member{GuildID: "g1", User: &discordgo.User{ID: "u2", Username: "u2name", GlobalName: "Global Two"}}); err != nil {
		t.Fatal(err)
	}
	return &discordgo.Session{State: st}
}

func TestStateGuilds_ManageServerOverREST(t *testing.T) {
	s := testSession(t)
	g := stateGuilds{s: s, api: fakeMembers{roles: map[string][]string{"g1/mod-user": {"mod"}, "g1/plain-user": {"plain"}, "g1/owner": {}}}}
	access := dashboard.GuildAccess{Allowed: []string{"listed"}, Guilds: g}
	for user, want := range map[string]bool{"mod-user": true, "plain-user": false, "owner": true, "stranger": false, "listed": true} {
		got, err := access.Authorize(context.Background(), user)
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", user, got, err, want)
		}
	}

	if ids := g.GuildIDs(); len(ids) != 1 || ids[0] != "g1" {
		t.Fatalf("guild IDs %v", ids)
	}
	if _, found, err := g.Member(context.Background(), "unknown-guild", "mod-user"); found || err != nil {
		t.Fatalf("unknown guild: %v %v", found, err)
	}
}

func TestStateGuilds_RESTFailureIsAnError(t *testing.T) {
	s := testSession(t)
	g := stateGuilds{s: s, api: fakeMembers{err: errors.New("HTTP 500")}}
	ok, err := dashboard.GuildAccess{Guilds: g}.Authorize(context.Background(), "mod-user")
	if ok || err == nil {
		t.Fatalf("got %v, %v; want a refusal with an error", ok, err)
	}
}

func TestStateNames(t *testing.T) {
	n := stateNames{s: testSession(t)}
	if n.Guild("g1") != "บ้านเพื่อน" || n.Channel("c1") != "แชท" || n.Member("g1", "u1") != "ชาย" || n.Member("g1", "u2") != "Global Two" {
		t.Fatalf("names: %q %q %q %q", n.Guild("g1"), n.Channel("c1"), n.Member("g1", "u1"), n.Member("g1", "u2"))
	}
	if n.Guild("x") != "" || n.Channel("x") != "" || n.Member("g1", "x") != "" {
		t.Fatal("unknown IDs should have no name")
	}
}

func TestDashboard_OffWithoutAddr(t *testing.T) {
	b, err := New(config.Config{DiscordToken: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.dash != nil {
		t.Fatal("server built without DASHBOARD_ADDR")
	}
}

func TestDashboard_PanelsAndHealth(t *testing.T) {
	logs := logring.New(10)
	b, err := New(config.Config{
		DiscordToken: "the-discord-bot-token",
		Dashboard:    config.DashboardConfig{Addr: "127.0.0.1:0"},
		Webhooks:     []config.WebhookConfig{{Name: "ci", Token: "ci-token-0123456789", ChannelID: "1"}},
	}, logs)
	if err != nil {
		t.Fatal(err)
	}
	if b.dash == nil || b.hooks == nil {
		t.Fatal("server or hooks not built")
	}

	var ids []string
	for _, p := range b.dashboardPanels() {
		ids = append(ids, p.ID)
	}
	if got := strings.Join(ids, ","); got != "bot,tts_sessions,tts_usage,events,webhooks,logs" {
		t.Fatalf("panels %s", got)
	}
	// TTS is off: the panels must say so rather than see a typed nil.
	for _, p := range b.dashboardPanels() {
		data, err := p.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		raw, _ := json.Marshal(data)
		if (p.ID == "tts_sessions" || p.ID == "tts_usage") && !strings.Contains(string(raw), `"enabled":false`) {
			t.Fatalf("%s: %s", p.ID, raw)
		}
		if p.ID == "webhooks" && !strings.Contains(string(raw), `"name":"ci"`) {
			t.Fatalf("webhooks: %s", raw)
		}
	}

	h := b.dash.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz before Ready: %d", rec.Code)
	}
	b.handleReady(nil, &discordgo.Ready{Application: &discordgo.Application{ID: "app-1"}})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || b.applicationID() != "app-1" {
		t.Fatalf("healthz after Ready: %d, app %q", rec.Code, b.applicationID())
	}
	b.handleDisconnect(nil, &discordgo.Disconnect{})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz after Disconnect: %d", rec.Code)
	}

	// Secrets are redacted in the log view.
	logs.Write([]byte("oops the-discord-bot-token and ci-token-0123456789\n"))
	if line := logs.Lines()[0].Text; strings.Contains(line, "the-discord-bot-token") || strings.Contains(line, "ci-token") {
		t.Fatalf("not redacted: %q", line)
	}
}
