package cover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Candidate is one picture a provider offers for a search.
type Candidate struct {
	Provider  string // "openverse", "pexels", "unsplash" or "brave"
	ID        string
	Title     string
	Creator   string
	License   string // as shown, e.g. "CC BY 2.0"
	SourceURL string // the picture's page, linked in the credit
	Source    string // Brave only: the page's domain, shown in the credit
	ImageURL  string // the full picture, downloaded for the cover
	ThumbURL  string // a small preview for the picker
	Width     int
	Height    int
	// DownloadLocation is Unsplash's endpoint that must be called when the
	// picture is used.
	DownloadLocation string
}

// Preferred is a landscape picture at least as wide as the cover.
func (c Candidate) Preferred() bool {
	return c.Width >= Width && c.Width > c.Height
}

// creditTitleMax keeps the credit to one short line of the description.
const creditTitleMax = 40

// Credit is the attribution line for the event description: title, creator,
// licence and a link to the source.
func (c Candidate) Credit() string {
	if c.Provider == "brave" {
		// An ordinary web image: say where it is from, claim no licence.
		return CreditPrefix + strings.Join([]string{c.Source, c.SourceURL}, " · ")
	}
	var parts []string
	title := strings.TrimSpace(c.Title)
	if r := []rune(title); len(r) > creditTitleMax {
		title = strings.TrimSpace(string(r[:creditTitleMax-1])) + "…"
	}
	head := ""
	switch {
	case title != "" && c.Creator != "":
		head = fmt.Sprintf("“%s” — %s", title, c.Creator)
	case title != "":
		head = fmt.Sprintf("“%s”", title)
	case c.Creator != "":
		head = c.Creator
	}
	if head != "" {
		parts = append(parts, head)
	}
	if c.License != "" {
		parts = append(parts, c.License)
	}
	if c.SourceURL != "" {
		parts = append(parts, c.SourceURL)
	}
	return CreditPrefix + strings.Join(parts, " · ")
}

// CreditPrefix starts the credit line, so it can be found and replaced when
// the cover changes.
const CreditPrefix = "📷 "

// Provider searches one image library.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error)
}

// UseTracker is implemented by providers that must be told when one of
// their pictures is used (Unsplash).
type UseTracker interface {
	TrackUse(ctx context.Context, c Candidate) error
}

// Chain asks providers in order until it has enough pictures.
type Chain []Provider

// NewChain puts the providers with an API key first (Pexels, Unsplash,
// Brave) and Openverse, which needs none, last as the fallback.
func NewChain(client *http.Client, pexelsKey, unsplashKey, braveKey string) Chain {
	var c Chain
	if pexelsKey != "" {
		c = append(c, &Pexels{Key: pexelsKey, Client: client})
	}
	if unsplashKey != "" {
		c = append(c, &Unsplash{Key: unsplashKey, Client: client})
	}
	if braveKey != "" {
		c = append(c, &Brave{Key: braveKey, Client: client})
	}
	return append(c, &Openverse{Client: client})
}

// Names lists the providers in the order they are tried.
func (c Chain) Names() []string {
	names := make([]string, len(c))
	for i, p := range c {
		names[i] = p.Name()
	}
	return names
}

// Search fills perPage slots from the providers in order, skipping pictures
// whose image URL was already taken. Within one provider, preferred pictures
// (landscape, ≥ 800 px wide) come first. A provider that fails is skipped;
// the error is returned only when nothing was found at all.
func (c Chain) Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error) {
	var errs []error
	var out []Candidate
	seen := map[string]bool{}
	for _, p := range c {
		if len(out) >= perPage {
			break
		}
		cands, err := p.Search(ctx, query, page, perPage)
		if err != nil {
			log.Printf("cover: %s search %q: %v", p.Name(), query, err)
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		cands = usable(cands)
		sort.SliceStable(cands, func(i, j int) bool {
			return cands[i].Preferred() && !cands[j].Preferred()
		})
		for _, cand := range cands {
			if len(out) >= perPage {
				break
			}
			if seen[cand.ImageURL] {
				continue
			}
			seen[cand.ImageURL] = true
			out = append(out, cand)
		}
	}
	if len(out) == 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Tracker returns the provider that must hear about c being used, if any.
func (c Chain) Tracker(cand Candidate) UseTracker {
	for _, p := range c {
		if t, ok := p.(UseTracker); ok && p.Name() == cand.Provider {
			return t
		}
	}
	return nil
}

func usable(cands []Candidate) []Candidate {
	out := cands[:0]
	for _, c := range cands {
		if c.ImageURL != "" && c.ThumbURL != "" {
			out = append(out, c)
		}
	}
	return out
}

const providerTimeout = 8 * time.Second

func getJSON(ctx context.Context, client *http.Client, url string, header http.Header, v any) error {
	if client == nil {
		client = &http.Client{Timeout: providerTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}
