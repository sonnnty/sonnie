package dashboard

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDocsStoreTOC(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}

	toc := ds.toc("00-overview")
	if len(toc) < 6 {
		t.Fatalf("expected at least 6 docs, got %d", len(toc))
	}

	expectedPrefixes := []string{
		"00-overview",
		"10-tts",
		"20-events",
		"30-dashboard",
		"40-webhooks",
		"90-troubleshooting",
	}

	for i, expected := range expectedPrefixes {
		if toc[i].Slug != expected {
			t.Errorf("doc[%d]: expected slug %q, got %q", i, expected, toc[i].Slug)
		}
		if toc[i].Title == "" {
			t.Errorf("doc[%d]: expected non-empty title", i)
		}
	}

	if !toc[0].Active {
		t.Errorf("expected 00-overview to be active")
	}
	if toc[1].Active {
		t.Errorf("expected 10-tts to not be active")
	}

	// Check active state changes when requesting another slug
	tocTTS := ds.toc("10-tts")
	if tocTTS[0].Active {
		t.Errorf("expected 00-overview to not be active when 10-tts requested")
	}
	if !tocTTS[1].Active {
		t.Errorf("expected 10-tts to be active")
	}
}

func TestDocsStore_EveryFileRenders(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}

	for _, item := range ds.items {
		html, doc, ok := ds.render(item.Slug)
		if !ok {
			t.Errorf("failed to render %s", item.Slug)
			continue
		}
		if doc.Title == "" {
			t.Errorf("empty title for %s", item.Slug)
		}
		htmlStr := string(html)
		if len(htmlStr) == 0 {
			t.Errorf("empty HTML for %s", item.Slug)
		}
		if !strings.Contains(htmlStr, "<h1") {
			t.Errorf("expected <h1> in %s", item.Slug)
		}
		if !strings.Contains(htmlStr, "class=\"anchor\"") {
			t.Errorf("expected anchor links in %s", item.Slug)
		}
	}
}

// GitHub alert syntax (> [!NOTE]) is not understood by the renderer and would
// show as literal text; the manual uses plain blockquotes instead.
func TestDocsStore_NoGitHubAlertSyntax(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}
	for _, item := range ds.items {
		html, _, ok := ds.render(item.Slug)
		if !ok {
			t.Errorf("failed to render %s", item.Slug)
			continue
		}
		if i := strings.Index(string(html), "[!"); i >= 0 {
			end := min(len(html), i+40)
			t.Errorf("%s: rendered page contains %q (GitHub alert syntax); use a plain blockquote such as `> **หมายเหตุ:** …`", item.Slug, string(html)[i:end])
		}
	}
}

func TestDocsStore_AliasesAndUnknown(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}

	// Test alias without numeric prefix
	_, item, ok := ds.render("tts")
	if !ok || item.Slug != "10-tts" {
		t.Errorf("expected alias 'tts' to resolve to '10-tts', got ok=%v item=%+v", ok, item)
	}

	// Test unknown slug
	_, _, okUnknown := ds.render("completely-unknown-topic")
	if okUnknown {
		t.Errorf("expected unknown slug to return false")
	}
}

func TestRawHTMLIsEscapedOrOmitted(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}

	rawInput := []byte("# Heading\n\n<script>alert('xss')</script>\n\n<b onmouseover='alert(1)'>bold</b>\n")
	rendered := ds.renderBytes(rawInput)
	s := string(rendered)
	if strings.Contains(s, "<script>") || strings.Contains(s, "</script>") {
		t.Errorf("raw HTML <script> should be escaped or omitted, got:\n%s", s)
	}
	if strings.Contains(s, "onmouseover") {
		t.Errorf("raw HTML attributes should be escaped or omitted, got:\n%s", s)
	}
}

func TestDocsHTTP_RequiresLogin(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()

	// Anonymous / not logged in
	resp, body := e.get(c, "/docs")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "เข้าสู่ระบบ") {
		t.Errorf("expected login page on /docs for anonymous user, got %d:\n%s", resp.StatusCode, body)
	}

	resp, body = e.get(c, "/docs/00-overview")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "เข้าสู่ระบบ") {
		t.Errorf("expected login page on /docs/00-overview for anonymous user, got %d:\n%s", resp.StatusCode, body)
	}

	// Forbidden user: user with a valid session cookie who is not authorized
	val, err := e.srv.sealer.sealSession(Session{
		UserID:  "3",
		Name:    "User 3",
		Issued:  e.clock.Now().Unix(),
		Expires: e.clock.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	u, _ := url.Parse(e.web.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: val, Path: "/"}})

	resp, body = e.get(c, "/docs")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "ไม่มีสิทธิ์") {
		t.Errorf("expected 403 on /docs for forbidden user, got %d:\n%s", resp.StatusCode, body)
	}

	resp, body = e.get(c, "/docs/00-overview")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "ไม่มีสิทธิ์") {
		t.Errorf("expected 403 on /docs/00-overview for forbidden user, got %d:\n%s", resp.StatusCode, body)
	}
}

func TestDocsHTTP_AuthorizedFlow(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()

	// Authorized user (user 1 is mod with Manage Server in guildA)
	e.login(c, "1")

	// /docs redirects to /docs/00-overview
	resp, _ := e.get(c, "/docs")
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 See Other on /docs, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/docs/00-overview" {
		t.Errorf("expected redirect to /docs/00-overview, got %q", loc)
	}

	// /docs/00-overview renders the page
	resp, body := e.get(c, "/docs/00-overview")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK on /docs/00-overview, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "ภาพรวมการใช้งาน SONNIE") {
		t.Errorf("expected body to contain title, got:\n%s", body)
	}
	if !strings.Contains(body, "สารบัญคู่มือ") {
		t.Errorf("expected body to contain TOC heading, got:\n%s", body)
	}
	if !strings.Contains(body, "คู่มือ") {
		t.Errorf("expected body to contain nav link, got:\n%s", body)
	}
}

func TestDocsHTTP_UnknownSlug404(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()

	e.login(c, "1")

	resp, body := e.get(c, "/docs/nonexistent-topic-slug")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found on unknown slug, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "ไม่พบคู่มือหน้านี้") {
		t.Errorf("expected 404 message, got:\n%s", body)
	}
}

func TestDocsHTTP_CSPHeaderPresent(t *testing.T) {
	e := newEnv(t, nil)
	c := e.browser()
	e.login(c, "1")

	paths := []string{"/docs", "/docs/00-overview", "/docs/nonexistent"}
	for _, p := range paths {
		resp, _ := e.get(c, p)
		csp := resp.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("path %s: missing Content-Security-Policy header", p)
		}
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("path %s: unexpected CSP: %s", p, csp)
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Errorf("path %s: CSP must not have unsafe-inline: %s", p, csp)
		}
	}
}
