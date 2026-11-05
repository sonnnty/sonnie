package dashboard

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/Sonty15/SONNIE/docs/manual"
)

// DocItem is one chapter in the manual's table of contents.
type DocItem struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Active   bool   `json:"active,omitempty"`
}

// customIDs generates URL-safe element IDs supporting Thai and alphanumeric characters.
type customIDs struct {
	used map[string]int
}

func newCustomIDs() parser.IDs {
	return &customIDs{used: make(map[string]int)}
}

func (s *customIDs) Put(value []byte) {
	s.used[string(value)] = 1
}

func (s *customIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	str := strings.TrimSpace(string(value))
	var b strings.Builder
	lastDash := false
	for _, r := range str {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == ':':
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		res = "heading"
	}
	count, exists := s.used[res]
	if !exists {
		s.used[res] = 1
		return []byte(res)
	}
	s.used[res] = count + 1
	return []byte(fmt.Sprintf("%s-%d", res, count))
}

// anchorTransformer prepends a clickable anchor link to every heading that has an id.
type anchorTransformer struct{}

func (a *anchorTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Kind() != ast.KindHeading {
			return ast.WalkContinue, nil
		}
		heading := n.(*ast.Heading)
		rawID, ok := heading.AttributeString("id")
		if !ok {
			return ast.WalkContinue, nil
		}
		id := string(rawID.([]byte))
		if id == "" {
			return ast.WalkContinue, nil
		}

		link := ast.NewLink()
		link.Destination = []byte("#" + id)
		link.SetAttribute([]byte("class"), []byte("anchor"))
		link.SetAttribute([]byte("aria-hidden"), []byte("true"))
		link.AppendChild(link, ast.NewString([]byte("#")))

		if heading.HasChildren() {
			heading.InsertBefore(heading, heading.FirstChild(), link)
		} else {
			heading.AppendChild(heading, link)
		}
		return ast.WalkContinue, nil
	})
}

// docsStore holds the loaded manual files and the markdown renderer.
type docsStore struct {
	mu     sync.RWMutex
	items  []DocItem
	bySlug map[string]DocItem
	raw    map[string][]byte
	md     goldmark.Markdown
}

func newDocsStore() (*docsStore, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(
				util.Prioritized(&anchorTransformer{}, 500),
			),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
		),
	)

	ds := &docsStore{
		bySlug: make(map[string]DocItem),
		raw:    make(map[string][]byte),
		md:     md,
	}

	entries, err := fs.ReadDir(manual.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("read manual docs: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := fs.ReadFile(manual.Files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read manual doc %s: %w", entry.Name(), err)
		}

		title := extractTitle(data)
		if title == "" {
			title = strings.TrimSuffix(entry.Name(), ".md")
		}

		primarySlug := strings.TrimSuffix(entry.Name(), ".md")
		item := DocItem{
			Slug:     primarySlug,
			Title:    title,
			Filename: entry.Name(),
		}

		ds.items = append(ds.items, item)
		ds.bySlug[primarySlug] = item
		ds.raw[primarySlug] = data

		// Also register trimmed alias (e.g. "tts" for "10-tts").
		trimmed := strings.TrimLeft(primarySlug, "0123456789-")
		if trimmed != "" && trimmed != primarySlug {
			ds.bySlug[trimmed] = item
			ds.raw[trimmed] = data
		}
	}

	sort.Slice(ds.items, func(i, j int) bool {
		return ds.items[i].Filename < ds.items[j].Filename
	})

	return ds, nil
}

func extractTitle(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// renderBytes converts markdown bytes to safe HTML with heading anchors.
func (ds *docsStore) renderBytes(content []byte) template.HTML {
	ctx := parser.NewContext(parser.WithIDs(newCustomIDs()))
	var buf bytes.Buffer
	if err := ds.md.Convert(content, &buf, parser.WithContext(ctx)); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}

// render renders the markdown file for slug to safe HTML.
func (ds *docsStore) render(slug string) (template.HTML, DocItem, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	item, ok := ds.bySlug[slug]
	if !ok {
		return "", DocItem{}, false
	}
	content := ds.raw[slug]
	return ds.renderBytes(content), item, true
}

func (ds *docsStore) toc(activeSlug string) []DocItem {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	out := make([]DocItem, len(ds.items))
	for i, it := range ds.items {
		it.Active = (it.Slug == activeSlug || strings.TrimLeft(it.Slug, "0123456789-") == activeSlug)
		out[i] = it
	}
	return out
}

func (ds *docsStore) firstSlug() string {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	if len(ds.items) == 0 {
		return ""
	}
	return ds.items[0].Slug
}
