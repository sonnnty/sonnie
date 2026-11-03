package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/readaloud"
)

// fakeSessionAPI serves message history from memory the way Discord does:
// pages come newest first, "after" returns the oldest messages after the ID.
type fakeSessionAPI struct {
	history map[string][]*discordgo.Message // channel -> ascending by ID
	perms   map[string]int64
	permErr error

	histCalls int
	listErr   error
	sent      []*discordgo.MessageSend
	edits     []*discordgo.MessageEdit
	sendErr   error
	editErr   error
	deleteErr error
	deleted   []string
	bulk      [][]string
}

func snow(n int) string { return strconv.Itoa(1_000_000_000_000_000_000 + n) }

func (f *fakeSessionAPI) put(channel string, m *discordgo.Message) {
	f.history[channel] = append(f.history[channel], m)
}

func (f *fakeSessionAPI) ChannelMessages(channelID string, limit int, beforeID, afterID, _ string, _ ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	f.histCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var sel []*discordgo.Message
	all := f.history[channelID]
	switch {
	case afterID != "":
		for _, m := range all {
			if snowflakeLess(afterID, m.ID) {
				sel = append(sel, m)
			}
		}
		if len(sel) > limit {
			sel = sel[:limit] // the oldest ones after the ID
		}
	default:
		for _, m := range all {
			if beforeID == "" || snowflakeLess(m.ID, beforeID) {
				sel = append(sel, m)
			}
		}
		if len(sel) > limit {
			sel = sel[len(sel)-limit:] // the newest ones before the ID
		}
	}
	out := append([]*discordgo.Message(nil), sel...)
	sort.Slice(out, func(i, j int) bool { return snowflakeLess(out[j].ID, out[i].ID) }) // newest first
	return out, nil
}

func (f *fakeSessionAPI) ChannelMessageSendComplex(_ string, d *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, d)
	return &discordgo.Message{ID: snow(5000 + len(f.sent))}, nil
}

func (f *fakeSessionAPI) ChannelMessageEditComplex(m *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.editErr != nil {
		return nil, f.editErr
	}
	f.edits = append(f.edits, m)
	return &discordgo.Message{ID: m.ID}, nil
}

func (f *fakeSessionAPI) ChannelMessageDelete(_, id string, _ ...discordgo.RequestOption) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func (f *fakeSessionAPI) ChannelMessagesBulkDelete(_ string, ids []string, _ ...discordgo.RequestOption) error {
	f.bulk = append(f.bulk, ids)
	return nil
}

func (f *fakeSessionAPI) UserChannelPermissions(_, channelID string, _ ...discordgo.RequestOption) (int64, error) {
	if f.permErr != nil {
		return 0, f.permErr
	}
	return f.perms[channelID], nil
}

func newTestMessenger(f *fakeSessionAPI, channels ...string) *discordMessenger {
	return &discordMessenger{
		api:      f,
		botID:    func() string { return "bot" },
		channels: func(string) []string { return channels },
	}
}

func newFakeSessionAPI() *fakeSessionAPI {
	return &fakeSessionAPI{history: map[string][]*discordgo.Message{}, perms: map[string]int64{}}
}

func userMsg(n int, author string) *discordgo.Message {
	return &discordgo.Message{ID: snow(n), Author: &discordgo.User{ID: author}, Type: discordgo.MessageTypeDefault}
}

func TestDiscordMessenger_MessagesAfterPagesOldestFirst(t *testing.T) {
	f := newFakeSessionAPI()
	for i := 1; i <= 250; i++ {
		f.put("c", userMsg(i, "u"))
	}
	m := newTestMessenger(f)

	got, err := m.MessagesAfter(context.Background(), "c", snow(0))

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 {
		t.Fatalf("got %d messages, want 250", len(got))
	}
	for i, msg := range got {
		if msg.ID != snow(i+1) {
			t.Fatalf("message %d is %s; not oldest first and complete", i, msg.ID)
		}
	}
	if f.histCalls != 3 {
		t.Fatalf("%d pages read, want 3", f.histCalls)
	}
	if want := time.UnixMilli(1_000_000_000_000_000_000>>22 + 1420070400000).UTC(); !got[0].Time.UTC().Equal(want) {
		t.Fatalf("time %v, want the snowflake time %v", got[0].Time, want)
	}
}

func TestDiscordMessenger_MessagesAfterSkipsSystemMessages(t *testing.T) {
	f := newFakeSessionAPI()
	f.put("c", userMsg(1, "u"))
	f.put("c", &discordgo.Message{ID: snow(2), Author: &discordgo.User{ID: "u"}, Type: discordgo.MessageTypeChannelPinnedMessage})
	f.put("c", &discordgo.Message{ID: snow(3), Author: &discordgo.User{ID: "u"}, Type: discordgo.MessageTypeReply})
	f.put("c", &discordgo.Message{ID: snow(4), Type: discordgo.MessageTypeDefault}) // no author
	m := newTestMessenger(f)

	got, err := m.MessagesAfter(context.Background(), "c", snow(0))
	if err != nil || len(got) != 2 || got[0].ID != snow(1) || got[1].ID != snow(3) {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestDiscordMessenger_MessagesAfterReturnsPartialResultOnError(t *testing.T) {
	f := newFakeSessionAPI()
	f.listErr = errors.New("missing access")
	got, err := newTestMessenger(f).MessagesAfter(context.Background(), "c", snow(0))
	if err == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDiscordMessenger_CanManageMessages(t *testing.T) {
	f := newFakeSessionAPI()
	f.perms["yes"] = discordgo.PermissionManageMessages | discordgo.PermissionViewChannel
	f.perms["admin"] = discordgo.PermissionAdministrator
	f.perms["no"] = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages
	m := newTestMessenger(f)

	for ch, want := range map[string]bool{"yes": true, "admin": true, "no": false, "unknown": false} {
		if got := m.CanManageMessages(ch); got != want {
			t.Errorf("channel %s: got %v, want %v", ch, got, want)
		}
	}
	f.permErr = errors.New("unknown channel")
	if m.CanManageMessages("yes") {
		t.Error("a permission lookup error must read as no permission")
	}
	m.botID = func() string { return "" }
	f.permErr = nil
	if m.CanManageMessages("yes") {
		t.Error("an unknown bot ID must read as no permission")
	}
}

func TestDiscordMessenger_PostAndEdit(t *testing.T) {
	f := newFakeSessionAPI()
	m := newTestMessenger(f)
	embed := &discordgo.MessageEmbed{Description: "x"}

	id, created, err := m.Post(context.Background(), "c", embed)
	if err != nil || id == "" || created.IsZero() {
		t.Fatalf("post: %q %v %v", id, created, err)
	}
	if len(f.sent) != 1 || f.sent[0].Embeds[0] != embed || f.sent[0].Content != "" {
		t.Fatalf("sent %+v", f.sent)
	}
	if f.sent[0].AllowedMentions == nil || len(f.sent[0].AllowedMentions.Parse) != 0 {
		t.Fatalf("the session message must not ping: %+v", f.sent[0].AllowedMentions)
	}

	if err := m.Edit(context.Background(), "c", id, embed); err != nil {
		t.Fatal(err)
	}
	e := f.edits[0]
	if e.ID != id || e.Channel != "c" || e.Content == nil || *e.Content != "" || len(*e.Embeds) != 1 {
		t.Fatalf("edit %+v", e)
	}

	f.sendErr = errors.New("missing permissions")
	if _, _, err := m.Post(context.Background(), "c", embed); err == nil {
		t.Fatal("post error lost")
	}
}

func TestDiscordMessenger_UnknownMessageMapsToGone(t *testing.T) {
	f := newFakeSessionAPI()
	m := newTestMessenger(f)
	unknown := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
	f.editErr, f.deleteErr = unknown, unknown

	if err := m.Edit(context.Background(), "c", "1", &discordgo.MessageEmbed{}); !errors.Is(err, readaloud.ErrMessageGone) {
		t.Errorf("edit: %v", err)
	}
	if err := m.DeleteMessage(context.Background(), "c", "1"); !errors.Is(err, readaloud.ErrMessageGone) {
		t.Errorf("delete: %v", err)
	}

	f.deleteErr = errors.New("forbidden")
	if err := m.DeleteMessage(context.Background(), "c", "1"); err == nil || errors.Is(err, readaloud.ErrMessageGone) {
		t.Errorf("other delete errors must stay errors: %v", err)
	}

	if err := m.BulkDelete(context.Background(), "c", []string{"1", "2"}); err != nil || len(f.bulk) != 1 {
		t.Errorf("bulk: %v %v", err, f.bulk)
	}
}

func sessionEmbedWithMarker(open bool, participants ...string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{Footer: &discordgo.MessageEmbedFooter{
		Text: "อัปเดตอัตโนมัติ\n" + readaloud.FormatSessionMarker(readaloud.SessionMarker{
			Open: open, Started: time.Unix(1793000000, 0), Voice: "v1", Manage: true, Participants: participants,
		}),
	}}
}

func TestDiscordMessenger_OpenSessions(t *testing.T) {
	const readable = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
	f := newFakeSessionAPI()
	f.perms = map[string]int64{"t1": readable, "t2": readable, "blind": discordgo.PermissionViewChannel}

	// t1: an open session, an ended one, an unrelated bot message, and an
	// open-looking embed quoted by a human (ignored).
	f.put("t1", &discordgo.Message{ID: snow(1), Author: &discordgo.User{ID: "bot"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(false, "u0")}})
	f.put("t1", &discordgo.Message{ID: snow(2), Author: &discordgo.User{ID: "bot"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(true, "u1", "u2")}})
	f.put("t1", &discordgo.Message{ID: snow(3), Author: &discordgo.User{ID: "bot"}, Content: "⚠️ Google TTS ใช้ไป 50%"})
	f.put("t1", &discordgo.Message{ID: snow(4), Author: &discordgo.User{ID: "human"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(true, "u9")}})
	f.put("t2", &discordgo.Message{ID: snow(5), Author: &discordgo.User{ID: "bot"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(true, "u3")}})
	f.put("blind", &discordgo.Message{ID: snow(6), Author: &discordgo.User{ID: "bot"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(true, "u4")}})
	m := newTestMessenger(f, "t1", "t2", "blind")

	got, err := m.OpenSessions(context.Background(), "g1")

	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range got {
		ids = append(ids, fmt.Sprintf("%s/%s/%s", o.GuildID, o.ChannelID, o.MessageID))
	}
	want := []string{"g1/t1/" + snow(2), "g1/t2/" + snow(5)}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("found %v, want %v", ids, want)
	}
	if o := got[0]; len(o.Marker.Participants) != 2 || o.Marker.Voice != "v1" || !o.Marker.Manage || o.Time.IsZero() {
		t.Fatalf("session %+v", o)
	}
}

func TestDiscordMessenger_OpenSessionsSearchesSeveralPages(t *testing.T) {
	f := newFakeSessionAPI()
	f.perms["t1"] = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
	f.put("t1", &discordgo.Message{ID: snow(1), Author: &discordgo.User{ID: "bot"}, Embeds: []*discordgo.MessageEmbed{sessionEmbedWithMarker(true, "u1")}})
	for i := 2; i <= 250; i++ { // 249 newer messages push it to page 3
		f.put("t1", userMsg(i, "u"))
	}
	got, err := newTestMessenger(f, "t1").OpenSessions(context.Background(), "g1")
	if err != nil || len(got) != 1 || got[0].MessageID != snow(1) {
		t.Fatalf("got %+v err %v", got, err)
	}
}
