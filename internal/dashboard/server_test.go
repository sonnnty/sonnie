package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testClientID     = "900000000000000001"
	testClientSecret = "client-secret-value"
	testKey          = "0123456789abcdef0123456789abcdef-test"
	guildA           = "100"
	guildB           = "200"
	roleMod          = "301"
	roleAdmin        = "302"
	roleMember       = "303"
)

// fakeDiscord is Discord's OAuth2 token endpoint and /users/@me. Codes map
// to users; each code works once.
type fakeDiscord struct {
	*httptest.Server
	mu        sync.Mutex
	codes     map[string]DiscordUser
	exchanges int
	lastForm  url.Values
	lastAuth  [2]string
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{codes: map[string]DiscordUser{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id, secret, _ := r.BasicAuth()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.exchanges++
		f.lastForm = r.PostForm
		f.lastAuth = [2]string{id, secret}
		u, ok := f.codes[r.PostForm.Get("code")]
		if !ok || id != testClientID || secret != testClientSecret || r.PostForm.Get("grant_type") != "authorization_code" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		delete(f.codes, r.PostForm.Get("code"))
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + u.ID, "token_type": "Bearer", "scope": "identify"})
	})
	mux.HandleFunc("GET /api/users/@me", func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer at-")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(DiscordUser{ID: id, Username: "user" + id, GlobalName: "Name " + id})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeDiscord) issue(code, userID string) {
	f.mu.Lock()
	f.codes[code] = DiscordUser{ID: userID}
	f.mu.Unlock()
}

func (f *fakeDiscord) endpoints() Endpoints {
	return Endpoints{
		AuthorizeURL: f.URL + "/oauth2/authorize",
		TokenURL:     f.URL + "/api/oauth2/token",
		UserURL:      f.URL + "/api/users/@me",
	}
}

// fakeGuilds: guild A has a moderator role with Manage Server and an admin
// role; guild B makes user 4 owner. Calls are counted to check the cache.
type fakeGuilds struct {
	mu      sync.Mutex
	members map[string]map[string][]string // guild → user → roles
	calls   int
	err     error
}

func newFakeGuilds() *fakeGuilds {
	return &fakeGuilds{members: map[string]map[string][]string{
		guildA: {"1": {roleMod}, "2": {roleAdmin}, "3": {roleMember}, "4": {}},
		guildB: {"3": {}, "4": {}},
	}}
}

func (g *fakeGuilds) GuildIDs() []string { return []string{guildA, guildB} }

func (g *fakeGuilds) Member(_ context.Context, guildID, userID string) (GuildMember, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.err != nil {
		return GuildMember{}, false, g.err
	}
	roles, ok := g.members[guildID][userID]
	if !ok {
		return GuildMember{}, false, nil
	}
	m := GuildMember{GuildID: guildID, MemberRoles: roles, RolePerms: map[string]int64{
		guildA:     1 << 10, // @everyone: View Channel
		guildB:     1 << 10,
		roleMod:    PermManageGuild | 1<<11,
		roleAdmin:  PermAdministrator,
		roleMember: 1 << 11,
	}}
	if guildID == guildB {
		m.OwnerID = "4"
	}
	return m, true, nil
}

func (g *fakeGuilds) setErr(err error) {
	g.mu.Lock()
	g.err = err
	g.mu.Unlock()
}

func (g *fakeGuilds) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t       *testing.T
	discord *fakeDiscord
	guilds  *fakeGuilds
	clock   *testClock
	srv     *Server
	web     *httptest.Server
	healthy atomic.Bool
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	e := &env{t: t, discord: newFakeDiscord(t), guilds: newFakeGuilds(), clock: &testClock{t: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}}
	cfg := Config{
		PublicURL:    "http://dash.test", // replaced below by the test server URL
		ClientID:     func() string { return testClientID },
		ClientSecret: testClientSecret,
		SessionKey:   []byte(testKey),
		Access:       GuildAccess{Allowed: []string{"9"}, Guilds: e.guilds},
		Healthy:      e.healthy.Load,
		Endpoints:    e.discord.endpoints(),
		HTTPClient:   e.discord.Client(),
		Now:          e.clock.Now,
		Panels: []Panel{
			{ID: "one", Title: "หนึ่ง", Snapshot: func(context.Context) (any, error) { return map[string]int{"n": 1}, nil }},
			{ID: "broken", Title: "พัง", Snapshot: func(context.Context) (any, error) { return nil, errors.New("boom") }},
			{ID: "panics", Title: "ตาย", Snapshot: func(context.Context) (any, error) { panic("oops") }},
		},
	}
	// The public URL must be the test server's, which exists only once the
	// handler does; route through a late-bound handler.
	var h http.Handler
	e.web = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	t.Cleanup(e.web.Close)
	cfg.PublicURL = e.web.URL
	if mod != nil {
		mod(&cfg)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = srv
	h = srv.Handler()
	return e
}

// browser is a client with a cookie jar that does not follow redirects.
func (e *env) browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) get(c *http.Client, path string) (*http.Response, string) {
	e.t.Helper()
	resp, err := c.Get(e.web.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// login runs the whole flow for userID and returns the callback answer.
func (e *env) login(c *http.Client, userID string) *http.Response {
	resp, _ := e.loginBody(c, userID)
	return resp
}

func (e *env) loginBody(c *http.Client, userID string) (*http.Response, string) {
	e.t.Helper()
	resp, _ := e.get(c, "/auth/login")
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("login: status %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	q := loc.Query()
	if !strings.HasPrefix(resp.Header.Get("Location"), e.discord.URL+"/oauth2/authorize?") ||
		q.Get("client_id") != testClientID || q.Get("scope") != "identify" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != e.web.URL+"/auth/callback" || len(q.Get("state")) < 32 {
		e.t.Fatalf("authorize URL %s", loc)
	}
	code := "code-" + userID
	e.discord.issue(code, userID)
	return e.get(c, "/auth/callback?code="+code+"&state="+url.QueryEscape(q.Get("state")))
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestHealthz(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	resp, body := e.get(c, "/healthz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disconnected: %d %q", resp.StatusCode, body)
	}
	e.healthy.Store(true)
	resp, body = e.get(c, "/healthz")
	if resp.StatusCode != http.StatusOK || body != "ok\n" {
		t.Fatalf("connected: %d %q", resp.StatusCode, body)
	}
}

func TestSecurityHeadersOnEveryAnswer(t *testing.T) {
	e := newEnv(t, nil)
	for _, path := range []string{"/", "/healthz", "/static/app.js", "/api/panels", "/nope"} {
		resp, _ := e.get(e.browser(), path)
		h := resp.Header
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP %q", path, csp)
		}
		if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") == "" {
			t.Errorf("%s: headers %v", path, h)
		}
	}
}

func TestStaticFilesAreEmbedded(t *testing.T) {
	e := newEnv(t, nil)
	for path, ctype := range map[string]string{"/static/app.js": "javascript", "/static/style.css": "text/css", "/static/favicon.svg": "image/svg+xml"} {
		resp, body := e.get(e.browser(), path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), ctype) || body == "" {
			t.Errorf("%s: %d %q", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestLoggedOutSeesLoginPage(t *testing.T) {
	e := newEnv(t, nil)
	resp, body := e.get(e.browser(), "/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "เข้าสู่ระบบด้วย Discord") || !strings.Contains(body, `href="/auth/login"`) {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "<script") {
		t.Fatal("the login page needs no script")
	}
	resp, _ = e.get(e.browser(), "/api/panels")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("API without login: %d", resp.StatusCode)
	}
}

func TestOAuth_ManageServerUserGetsIn(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	cb := e.login(c, "1")
	if cb.StatusCode != http.StatusSeeOther || cb.Header.Get("Location") != "/" {
		t.Fatalf("callback %d %s", cb.StatusCode, cb.Header.Get("Location"))
	}
	sc := cookieNamed(cb, sessionCookie)
	if sc == nil || !sc.HttpOnly || sc.Secure || sc.SameSite != http.SameSiteLaxMode || sc.MaxAge != int(SessionTTL/time.Second) {
		t.Fatalf("session cookie %+v", sc)
	}
	if strings.Contains(sc.Value, "1") && strings.Contains(sc.Value, "Name") {
		t.Fatal("session cookie is not encrypted")
	}
	if st := cookieNamed(cb, stateCookie); st == nil || st.MaxAge >= 0 {
		t.Fatalf("state cookie not cleared: %+v", st)
	}
	if e.discord.lastForm.Get("redirect_uri") != e.web.URL+"/auth/callback" || e.discord.lastAuth[1] != testClientSecret {
		t.Fatalf("token exchange form %v", e.discord.lastForm)
	}

	resp, body := e.get(c, "/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Name 1") || !strings.Contains(body, `id="panel-one"`) || !strings.Contains(body, `src="/static/app.js"`) {
		t.Fatalf("dashboard %d %s", resp.StatusCode, body)
	}

	resp, body = e.get(c, "/api/panels")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API %d %s", resp.StatusCode, body)
	}
	var out struct {
		Panels []PanelResult `json:"panels"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Panels) != 3 {
		t.Fatalf("API body %s", body)
	}
	if out.Panels[0].ID != "one" || out.Panels[0].Error != "" || out.Panels[0].Data.(map[string]any)["n"] != float64(1) {
		t.Fatalf("panel one %+v", out.Panels[0])
	}
	if out.Panels[1].Error != "boom" || !strings.Contains(out.Panels[2].Error, "oops") {
		t.Fatalf("failing panels %+v", out.Panels[1:])
	}
}

func TestOAuth_WhoGetsIn(t *testing.T) {
	cases := []struct {
		user string
		in   bool
		why  string
	}{
		{"1", true, "Manage Server role"},
		{"2", true, "Administrator role"},
		{"3", false, "plain member in both guilds"},
		{"4", true, "owner of guild B"},
		{"5", false, "in no guild of the bot"},
		{"9", true, "listed in DASHBOARD_ALLOWED_USERS"},
	}
	for _, tc := range cases {
		e := newEnv(t, nil)
		c := e.browser()
		cb := e.login(c, tc.user)
		if tc.in {
			if cb.StatusCode != http.StatusSeeOther || cookieNamed(cb, sessionCookie) == nil {
				t.Errorf("user %s (%s): want in, got %d", tc.user, tc.why, cb.StatusCode)
			}
			continue
		}
		if cb.StatusCode != http.StatusForbidden || cookieNamed(cb, sessionCookie) != nil {
			t.Errorf("user %s (%s): want 403 without a session, got %d", tc.user, tc.why, cb.StatusCode)
		}
	}
}

func TestOAuth_ForbiddenPageIsPoliteThai(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	cb, body := e.loginBody(c, "3")
	if cb.StatusCode != http.StatusForbidden || !strings.Contains(body, "ขออภัย") || !strings.Contains(body, "Manage Server") {
		t.Fatalf("got %d %s", cb.StatusCode, body)
	}
}

func TestOAuth_StateMismatchIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.get(c, "/auth/login") // sets the state cookie
	e.discord.issue("code-1", "1")
	for _, q := range []string{"code=code-1&state=forged", "code=code-1", "code=code-1&state="} {
		resp, body := e.get(c, "/auth/callback?"+q)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "ไม่ถูกต้อง") || cookieNamed(resp, sessionCookie) != nil {
			t.Fatalf("%s: got %d", q, resp.StatusCode)
		}
	}
	// Without any state cookie (a link from elsewhere) even a matching value fails.
	resp, _ := e.get(e.browser(), "/auth/callback?code=code-1&state=abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no cookie: %d", resp.StatusCode)
	}
	if e.discord.exchanges != 0 {
		t.Fatalf("exchanged %d codes on a bad state", e.discord.exchanges)
	}
}

func TestOAuth_StateIsSingleUse(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	resp, _ := e.get(c, "/auth/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	e.discord.issue("bad-code-is-not-issued", "1")
	if r, _ := e.get(c, "/auth/callback?code=wrong&state="+url.QueryEscape(state)); r.StatusCode != http.StatusBadGateway {
		t.Fatalf("bad code: %d", r.StatusCode)
	}
	e.discord.issue("code-1", "1")
	if r, _ := e.get(c, "/auth/callback?code=code-1&state="+url.QueryEscape(state)); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed state: %d", r.StatusCode)
	}
}

func TestOAuth_DiscordErrorAndMissingCode(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	resp, body := e.get(c, "/auth/callback?error=access_denied&state=x")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "ยกเลิก") {
		t.Fatalf("error=: %d", resp.StatusCode)
	}
	r, _ := e.get(c, "/auth/login")
	loc, _ := url.Parse(r.Header.Get("Location"))
	resp, _ = e.get(c, "/auth/callback?state="+url.QueryEscape(loc.Query().Get("state")))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no code: %d", resp.StatusCode)
	}
}

func TestSession_TamperedOrForeignCookieIsIgnored(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	cb := e.login(c, "1")
	good := cookieNamed(cb, sessionCookie).Value

	flip := []byte(good)
	flip[len(flip)/2] ^= 0x01
	if flip[len(flip)/2] == '-' || flip[len(flip)/2] == '_' {
		flip[len(flip)/2] = 'A'
	}
	other, _ := newSealer([]byte("another-key-another-key-another-key!"), sessionCookie)
	foreign, _ := other.sealSession(Session{UserID: "1", Name: "x", Expires: e.clock.Now().Add(time.Hour).Unix()})
	sameKeyOtherPurpose, _ := newSealer([]byte(testKey), "other-purpose")
	replayed, _ := sameKeyOtherPurpose.sealSession(Session{UserID: "1", Name: "x", Expires: e.clock.Now().Add(time.Hour).Unix()})

	for name, value := range map[string]string{
		"bit flip":      string(flip),
		"truncated":     good[:len(good)-4],
		"garbage":       "not-a-cookie",
		"empty":         "",
		"other key":     foreign,
		"other purpose": replayed,
	} {
		req, _ := http.NewRequest(http.MethodGet, e.web.URL+"/api/panels", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
}

func TestSession_ExpiresAfterSevenDays(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")
	e.clock.advance(SessionTTL - time.Minute)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusOK {
		t.Fatalf("before expiry: %d", resp.StatusCode)
	}
	e.clock.advance(2 * time.Minute)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after expiry: %d", resp.StatusCode)
	}
}

func TestAccess_RecheckedAtMostEveryTenMinutes(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")
	after := e.guilds.callCount()
	for i := 0; i < 5; i++ {
		e.get(c, "/api/panels")
		e.clock.advance(time.Minute)
	}
	if got := e.guilds.callCount(); got != after {
		t.Fatalf("re-checked within the cache window: %d calls", got-after)
	}

	// The user loses the role: refused once the cache runs out.
	e.guilds.mu.Lock()
	e.guilds.members[guildA]["1"] = []string{roleMember}
	e.guilds.mu.Unlock()
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusOK {
		t.Fatalf("still cached: %d", resp.StatusCode)
	}
	e.clock.advance(6 * time.Minute)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("after losing the role: %d", resp.StatusCode)
	}
	if resp, body := e.get(c, "/"); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "ขออภัย") {
		t.Fatalf("page after losing the role: %d", resp.StatusCode)
	}
}

func TestAccess_OutageUsesRecentDecision(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")
	e.guilds.setErr(errors.New("discord down"))
	e.clock.advance(11 * time.Minute)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusOK {
		t.Fatalf("stale but recent decision: %d", resp.StatusCode)
	}
	e.clock.advance(time.Hour)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("too old to trust: %d", resp.StatusCode)
	}
}

func TestAccess_UnloadedGuildsAreRetriedNotCached(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")
	// The bot restarted long ago enough that nothing cached is trusted, and
	// its guilds are not loaded yet.
	e.clock.advance(2 * time.Hour)
	e.guilds.setErr(fmt.Errorf("%w: guild 100", ErrGuildsNotLoaded))
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("while loading: %d, want 503 (the page retries)", resp.StatusCode)
	}
	// The guilds arrive: the very next poll gets in, no 10-minute refusal.
	e.guilds.setErr(nil)
	if resp, _ := e.get(c, "/api/panels"); resp.StatusCode != http.StatusOK {
		t.Fatalf("after loading: %d", resp.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")
	req, _ := http.NewRequest(http.MethodPost, e.web.URL+"/auth/logout", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || cookieNamed(resp, sessionCookie) == nil || cookieNamed(resp, sessionCookie).MaxAge >= 0 {
		t.Fatalf("logout %d %v", resp.StatusCode, resp.Cookies())
	}
	if r, _ := e.get(c, "/api/panels"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", r.StatusCode)
	}
}

func TestSecureCookiesOnHTTPS(t *testing.T) {
	e := newEnv(t, nil)
	srv, err := New(Config{
		PublicURL: "https://dash.example", ClientID: func() string { return testClientID },
		ClientSecret: testClientSecret, SessionKey: []byte(testKey), Endpoints: e.discord.endpoints(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	resp := rec.Result()
	if st := cookieNamed(resp, stateCookie); st == nil || !st.Secure || !st.HttpOnly {
		t.Fatalf("state cookie %+v", st)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("redirect_uri") != "https://dash.example/auth/callback" {
		t.Fatalf("redirect URI %s", loc.Query().Get("redirect_uri"))
	}
}

func TestLoginNotConfigured(t *testing.T) {
	var hits atomic.Int32
	srv, err := New(Config{
		Healthy:  func() bool { return true },
		Webhooks: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.LoginEnabled() {
		t.Fatal("login enabled without settings")
	}
	for _, path := range []string{"/", "/auth/login", "/auth/callback?code=x&state=y"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "DASHBOARD_SESSION_KEY") {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hooks/x", nil))
	if hits.Load() != 1 {
		t.Fatal("webhooks not mounted without login")
	}
}

func TestLoginWaitsForClientID(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ClientID = func() string { return "" } })
	resp, body := e.get(e.browser(), "/auth/login")
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "DISCORD_CLIENT_ID") {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestStartAndShutdown(t *testing.T) {
	srv, err := New(Config{Addr: "127.0.0.1:0", Healthy: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
