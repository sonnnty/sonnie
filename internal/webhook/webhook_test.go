package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	goodToken = "grafana-token-0123456789"
	ciToken   = "ci-token-abcdefghijklmn"
)

type sent struct {
	channelID string
	msg       *discordgo.MessageSend
}

type fakePoster struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (p *fakePoster) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	p.sent = append(p.sent, sent{channelID, data})
	return &discordgo.Message{ID: fmt.Sprintf("m%d", len(p.sent)), ChannelID: channelID}, nil
}

func (p *fakePoster) last(t *testing.T) sent {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sent) == 0 {
		t.Fatal("nothing was posted")
	}
	return p.sent[len(p.sent)-1]
}

func (p *fakePoster) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	h     *Handler
	p     *fakePoster
	clock *clock
	mux   *http.ServeMux
}

func newEnv(t *testing.T, rate int) *env {
	t.Helper()
	p := &fakePoster{}
	c := &clock{t: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	h := New(Config{
		Hooks: []Hook{
			{Name: "grafana", Token: goodToken, ChannelID: "111"},
			{Name: "ci", Token: ciToken, ChannelID: "222"},
		},
		Poster:    p,
		RateLimit: rate,
		Now:       c.Now,
	})
	mux := http.NewServeMux()
	mux.Handle("POST /hooks/{name}", h)
	return &env{h: h, p: p, clock: c, mux: mux}
}

type reply struct {
	code   int
	header http.Header
	body   map[string]any
}

func (e *env) post(t *testing.T, path, contentType, body string, headers ...string) reply {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = "10.0.0.7:5555"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q", rec.Body.String())
	}
	return reply{rec.Code, rec.Header(), out}
}

func bearer(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

func TestWebhook_GoodTokenPostsToTheHookChannel(t *testing.T) {
	e := newEnv(t, 0)
	r := e.post(t, "/hooks/grafana", "application/json", `{"content":"hello"}`, bearer(goodToken)...)
	if r.code != http.StatusOK || r.body["ok"] != true || r.body["message_id"] != "m1" || r.body["channel_id"] != "111" {
		t.Fatalf("got %d %v", r.code, r.body)
	}
	s := e.p.last(t)
	if s.channelID != "111" || s.msg.Content != "hello" {
		t.Fatalf("posted %+v", s)
	}

	// The other header works too, and routes to the other hook's channel.
	r = e.post(t, "/hooks/ci", "text/plain", "build ok", "X-Sonnie-Token", ciToken)
	if r.code != http.StatusOK || e.p.last(t).channelID != "222" {
		t.Fatalf("X-Sonnie-Token: %d %v", r.code, r.body)
	}
}

func TestWebhook_BadTokenAndUnknownHookLookTheSame(t *testing.T) {
	e := newEnv(t, 0)
	cases := []struct {
		name, path string
		headers    []string
	}{
		{"wrong token", "/hooks/grafana", bearer("wrong-token-0123456789")},
		{"other hook's token", "/hooks/grafana", bearer(ciToken)},
		{"no token", "/hooks/grafana", nil},
		{"empty bearer", "/hooks/grafana", []string{"Authorization", "Bearer "}},
		{"basic auth", "/hooks/grafana", []string{"Authorization", "Basic " + goodToken}},
		{"unknown hook", "/hooks/nope", bearer(goodToken)},
	}
	var first map[string]any
	for _, c := range cases {
		r := e.post(t, c.path, "application/json", `{"content":"x"}`, c.headers...)
		if r.code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", c.name, r.code)
		}
		if first == nil {
			first = r.body
		} else if fmt.Sprint(r.body) != fmt.Sprint(first) {
			t.Errorf("%s: body %v differs from %v", c.name, r.body, first)
		}
	}
	if e.p.count() != 0 {
		t.Fatalf("posted %d messages", e.p.count())
	}
}

func TestWebhook_GetIsNotAllowed(t *testing.T) {
	e := newEnv(t, 0)
	req := httptest.NewRequest(http.MethodGet, "/hooks/grafana", nil)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestWebhook_PayloadVariants(t *testing.T) {
	cases := []struct {
		name, ctype, body string
		check             func(t *testing.T, m *discordgo.MessageSend)
	}{
		{"discord content and embed", "application/json", `{"content":"deploy","embeds":[{"title":"T","description":"D","color":255,"fields":[{"name":"a","value":"b","inline":true}]}]}`,
			func(t *testing.T, m *discordgo.MessageSend) {
				if m.Content != "deploy" || len(m.Embeds) != 1 || m.Embeds[0].Title != "T" || m.Embeds[0].Color != 255 || len(m.Embeds[0].Fields) != 1 || !m.Embeds[0].Fields[0].Inline {
					t.Fatalf("got %+v", m)
				}
			}},
		{"simple form", "application/json; charset=utf-8", `{"title":"CPU high","text":"node1 95%","color":"#ff8800","url":"https://grafana.example/d/1"}`,
			func(t *testing.T, m *discordgo.MessageSend) {
				e := m.Embeds[0]
				if m.Content != "" || e.Title != "CPU high" || e.Description != "node1 95%" || e.Color != 0xff8800 || e.URL != "https://grafana.example/d/1" || e.Footer.Text != "webhook · grafana" {
					t.Fatalf("got %+v %+v", m, e)
				}
			}},
		{"simple form numeric color", "application/json", `{"text":"x","color":65280}`,
			func(t *testing.T, m *discordgo.MessageSend) {
				if m.Embeds[0].Color != 65280 {
					t.Fatalf("color %d", m.Embeds[0].Color)
				}
			}},
		{"text plain", "text/plain; charset=utf-8", "  plain text  ",
			func(t *testing.T, m *discordgo.MessageSend) {
				if m.Content != "plain text" || len(m.Embeds) != 0 {
					t.Fatalf("got %+v", m)
				}
			}},
		{"no content type, JSON body", "", `{"content":"sniffed"}`,
			func(t *testing.T, m *discordgo.MessageSend) {
				if m.Content != "sniffed" {
					t.Fatalf("got %+v", m)
				}
			}},
		{"no content type, text body", "", `just words`,
			func(t *testing.T, m *discordgo.MessageSend) {
				if m.Content != "just words" {
					t.Fatalf("got %+v", m)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, 0)
			r := e.post(t, "/hooks/grafana", c.ctype, c.body, bearer(goodToken)...)
			if r.code != http.StatusOK {
				t.Fatalf("status %d %v", r.code, r.body)
			}
			c.check(t, e.p.last(t).msg)
		})
	}
}

func TestWebhook_RejectsBadPayloads(t *testing.T) {
	cases := []struct {
		name, ctype, body string
		want              int
	}{
		{"broken JSON", "application/json", `{"content":`, http.StatusBadRequest},
		{"empty JSON", "application/json", `{}`, http.StatusBadRequest},
		{"only blanks", "text/plain", "   ", http.StatusBadRequest},
		{"bad color", "application/json", `{"text":"x","color":"orange"}`, http.StatusBadRequest},
		{"color out of range", "application/json", `{"text":"x","color":16777216}`, http.StatusBadRequest},
		{"embeds that sanitise to nothing", "application/json", `{"embeds":[{"url":"https://x"},{"image":{"url":"javascript:alert(1)"}}]}`, http.StatusBadRequest},
		{"wrong type", "application/xml", `<a/>`, http.StatusUnsupportedMediaType},
		{"not UTF-8", "text/plain", "\xff\xfe", http.StatusBadRequest},
	}
	for _, c := range cases {
		e := newEnv(t, 0)
		r := e.post(t, "/hooks/grafana", c.ctype, c.body, bearer(goodToken)...)
		if r.code != c.want || r.body["ok"] != false {
			t.Errorf("%s: got %d %v, want %d", c.name, r.code, r.body, c.want)
		}
		if e.p.count() != 0 {
			t.Errorf("%s: posted anyway", c.name)
		}
	}
}

func TestWebhook_NeverPings(t *testing.T) {
	for _, body := range []string{
		`{"content":"@everyone <@&123> <@456> look"}`,
		`{"title":"@here","text":"<@456>"}`,
	} {
		e := newEnv(t, 0)
		if r := e.post(t, "/hooks/grafana", "application/json", body, bearer(goodToken)...); r.code != http.StatusOK {
			t.Fatalf("%s: %d", body, r.code)
		}
		am := e.p.last(t).msg.AllowedMentions
		if am == nil || am.Parse == nil || len(am.Parse) != 0 || len(am.Users) != 0 || len(am.Roles) != 0 {
			t.Fatalf("allowed mentions %+v", am)
		}
		raw, _ := json.Marshal(am)
		if !strings.Contains(string(raw), `"parse":[]`) {
			t.Fatalf("serialised as %s, want parse: []", raw)
		}
	}
}

func TestWebhook_SizeLimit(t *testing.T) {
	e := newEnv(t, 0)
	big := `{"content":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	r := e.post(t, "/hooks/grafana", "application/json", big, bearer(goodToken)...)
	if r.code != http.StatusRequestEntityTooLarge || e.p.count() != 0 {
		t.Fatalf("got %d, posted %d", r.code, e.p.count())
	}
	// Just under the limit is accepted (the content is then cut to 2000).
	ok := `{"content":"` + strings.Repeat("a", MaxBodyBytes-20) + `"}`
	if r := e.post(t, "/hooks/grafana", "application/json", ok, bearer(goodToken)...); r.code != http.StatusOK {
		t.Fatalf("under the limit: %d %v", r.code, r.body)
	}
	if got := []rune(e.p.last(t).msg.Content); len(got) != maxContent || got[len(got)-1] != '…' {
		t.Fatalf("content not cut to %d: %d", maxContent, len(got))
	}
}

func TestWebhook_RateLimitPerHook(t *testing.T) {
	e := newEnv(t, 3)
	for i := 0; i < 3; i++ {
		if r := e.post(t, "/hooks/grafana", "text/plain", "x", bearer(goodToken)...); r.code != http.StatusOK {
			t.Fatalf("call %d: %d", i, r.code)
		}
		e.clock.advance(10 * time.Second)
	}
	r := e.post(t, "/hooks/grafana", "text/plain", "x", bearer(goodToken)...)
	if r.code != http.StatusTooManyRequests || r.header.Get("Retry-After") != "30" {
		t.Fatalf("4th call: %d retry-after %q", r.code, r.header.Get("Retry-After"))
	}
	// Another hook has its own budget.
	if r := e.post(t, "/hooks/ci", "text/plain", "x", bearer(ciToken)...); r.code != http.StatusOK {
		t.Fatalf("other hook: %d", r.code)
	}
	// Bad tokens do not use up the budget of a hook.
	e.clock.advance(31 * time.Second) // the first call is now over a minute old
	for i := 0; i < 5; i++ {
		e.post(t, "/hooks/grafana", "text/plain", "x", bearer("wrong-token-0123456789")...)
	}
	if r := e.post(t, "/hooks/grafana", "text/plain", "x", bearer(goodToken)...); r.code != http.StatusOK {
		t.Fatalf("after the window moved: %d", r.code)
	}
}

func TestWebhook_DiscordFailureIs502(t *testing.T) {
	e := newEnv(t, 0)
	e.p.err = errors.New("HTTP 403 Forbidden, Missing Access")
	r := e.post(t, "/hooks/grafana", "text/plain", "x", bearer(goodToken)...)
	if r.code != http.StatusBadGateway || strings.Contains(fmt.Sprint(r.body), "Missing Access") {
		t.Fatalf("got %d %v", r.code, r.body)
	}
}

func TestWebhook_StatusRecordsCallsWithoutTokens(t *testing.T) {
	e := newEnv(t, 0)
	e.post(t, "/hooks/grafana", "text/plain", "hello", bearer(goodToken)...)
	e.clock.advance(time.Second)
	e.post(t, "/hooks/grafana", "text/plain", "x", bearer("wrong-token-0123456789")...)
	e.clock.advance(time.Second)
	e.post(t, "/hooks/"+strings.Repeat("z", 50), "text/plain", "x", bearer(goodToken)...)

	st := e.h.Status()
	if st.RateLimit != DefaultRateLimit || st.MaxBodyBytes != MaxBodyBytes || len(st.Hooks) != 2 {
		t.Fatalf("status %+v", st)
	}
	g := st.Hooks[0]
	if g.Name != "grafana" || g.ChannelID != "111" || g.Calls != 1 || g.LastStatus != http.StatusOK || g.LastCall == nil {
		t.Fatalf("grafana %+v", g)
	}
	if st.Hooks[1].Calls != 0 || st.Hooks[1].LastCall != nil {
		t.Fatalf("ci %+v", st.Hooks[1])
	}
	if len(st.Recent) != 3 {
		t.Fatalf("recent %+v", st.Recent)
	}
	newest, oldest := st.Recent[0], st.Recent[2]
	if len(newest.Hook) != maxShownName || newest.Known || newest.Status != http.StatusUnauthorized {
		t.Fatalf("newest %+v", newest)
	}
	if oldest.Hook != "grafana" || !oldest.Known || oldest.Status != 200 || oldest.SourceIP != "10.0.0.7" || oldest.MessageID != "m1" || oldest.Bytes != 5 {
		t.Fatalf("oldest %+v", oldest)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), goodToken) || strings.Contains(string(raw), ciToken) {
		t.Fatalf("status leaks a token: %s", raw)
	}
}

func TestWebhook_RecentCallsAreBounded(t *testing.T) {
	e := newEnv(t, 1000)
	for i := 0; i < recentCalls+10; i++ {
		e.post(t, "/hooks/grafana", "text/plain", fmt.Sprint(i), bearer(goodToken)...)
		e.clock.advance(time.Millisecond)
	}
	st := e.h.Status()
	if len(st.Recent) != recentCalls || st.Recent[0].MessageID != fmt.Sprintf("m%d", recentCalls+10) {
		t.Fatalf("got %d calls, newest %+v", len(st.Recent), st.Recent[0])
	}
	if st.Hooks[0].Calls != recentCalls+10 {
		t.Fatalf("calls %d", st.Hooks[0].Calls)
	}
}
