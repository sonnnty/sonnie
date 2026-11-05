package dashboard

import (
	"strings"
	"testing"
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
}

func TestDocsStoreRender(t *testing.T) {
	ds, err := newDocsStore()
	if err != nil {
		t.Fatalf("newDocsStore: %v", err)
	}

	html, item, ok := ds.render("00-overview")
	if !ok {
		t.Fatalf("failed to render 00-overview")
	}
	if item.Title == "" {
		t.Fatalf("item title is empty")
	}
	htmlStr := string(html)
	if !strings.Contains(htmlStr, "<h1") {
		t.Errorf("expected rendered HTML to contain <h1>, got:\n%s", htmlStr)
	}

	// Test alias
	_, _, okAlias := ds.render("overview")
	if !okAlias {
		t.Errorf("expected alias 'overview' to render")
	}

	// Test unknown slug
	_, _, okUnknown := ds.render("nonexistent")
	if okUnknown {
		t.Errorf("expected nonexistent slug to return false")
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
