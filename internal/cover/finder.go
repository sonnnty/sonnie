package cover

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ErrNoCover means no picture could be found or used.
var ErrNoCover = errors.New("cover: no usable picture found")

// KeywordTimeout bounds the Gemini call; the raw name is used after it.
const KeywordTimeout = 3 * time.Second

const (
	// autoCandidates is how many results /event create looks at.
	autoCandidates = 8
	// autoTries is how many of them it tries to download before giving up.
	autoTries = 4
)

// Cover is a processed cover ready for Discord.
type Cover struct {
	JPEG []byte
	// Credit is the attribution line, empty for an uploaded attachment.
	Credit string
}

// Finder searches the providers, downloads a picture and turns it into a
// cover.
type Finder struct {
	Providers Chain
	// Keywords translates Thai names; nil uses the name as it is.
	Keywords Keyworder
	// Client downloads pictures; nil uses a client with a timeout.
	Client *http.Client
}

// Query is what to search for: the keywords the user gave, else the event
// name, translated to English keywords when it is Thai. The translation
// falls back to the name on any failure, after one log line.
func (f *Finder) Query(ctx context.Context, name, keywords string) string {
	if kw := strings.TrimSpace(keywords); kw != "" {
		return kw
	}
	name = strings.TrimSpace(name)
	if f.Keywords == nil || !HasThai(name) {
		return name
	}
	ctx, cancel := context.WithTimeout(ctx, KeywordTimeout)
	defer cancel()
	start := time.Now()
	kw, err := f.Keywords.Keywords(ctx, name)
	ms := time.Since(start).Milliseconds()
	switch {
	case errors.Is(err, ErrDisabled):
		// Logged when it was first rejected.
		return name
	case errors.Is(err, ErrRejected):
		log.Printf("cover: gemini keywords for %q: %v; searching by event name from now on", name, err)
		return name
	case err != nil:
		log.Printf("cover: gemini keywords for %q failed after %d ms: %v; searching by event name", name, ms, err)
		return name
	}
	log.Printf("cover: gemini keywords for %q: %q (%d ms)", name, kw, ms)
	return kw
}

// Search returns up to n candidates for query; page starts at 1.
func (f *Finder) Search(ctx context.Context, query string, page, n int) ([]Candidate, error) {
	cands, err := f.Providers.Search(ctx, query, page, n)
	if len(cands) > n {
		cands = cands[:n]
	}
	return cands, err
}

// Fetch downloads and processes c, and tells its provider it is used.
func (f *Finder) Fetch(ctx context.Context, c Candidate) (Cover, error) {
	data, err := Download(ctx, f.Client, c.ImageURL)
	if err != nil {
		return Cover{}, err
	}
	out, err := Process(data)
	if err != nil {
		return Cover{}, err
	}
	if t := f.Providers.Tracker(c); t != nil {
		if err := t.TrackUse(ctx, c); err != nil {
			log.Printf("cover: %s: report use of %s: %v", c.Provider, c.ID, err)
		}
	}
	return Cover{JPEG: out, Credit: c.Credit()}, nil
}

// Auto picks the first of the top results that downloads and decodes.
func (f *Finder) Auto(ctx context.Context, query string) (Cover, error) {
	cands, err := f.Search(ctx, query, 1, autoCandidates)
	if len(cands) == 0 {
		if err != nil {
			return Cover{}, fmt.Errorf("%w: %v", ErrNoCover, err)
		}
		return Cover{}, ErrNoCover
	}
	for i, c := range cands {
		if i == autoTries {
			break
		}
		cov, err := f.Fetch(ctx, c)
		if err == nil {
			return cov, nil
		}
		log.Printf("cover: %s picture %s: %v", c.Provider, c.ID, err)
	}
	return Cover{}, ErrNoCover
}

// FromURL turns an uploaded attachment into a cover.
func (f *Finder) FromURL(ctx context.Context, url string) (Cover, error) {
	data, err := Download(ctx, f.Client, url)
	if err != nil {
		return Cover{}, err
	}
	out, err := Process(data)
	if err != nil {
		return Cover{}, err
	}
	return Cover{JPEG: out}, nil
}
