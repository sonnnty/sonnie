package cover

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Pexels searches pexels.com (PEXELS_API_KEY). All Pexels photos are under
// the Pexels License, which allows reuse; the photographer is credited.
type Pexels struct {
	Key     string
	BaseURL string // default https://api.pexels.com
	Client  *http.Client
}

func (p *Pexels) Name() string { return "pexels" }

type pexelsResponse struct {
	Photos []struct {
		ID           int64  `json:"id"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		URL          string `json:"url"`
		Alt          string `json:"alt"`
		Photographer string `json:"photographer"`
		Src          struct {
			Large2x string `json:"large2x"`
			Medium  string `json:"medium"`
		} `json:"src"`
	} `json:"photos"`
}

func (p *Pexels) Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://api.pexels.com"
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("orientation", "landscape")
	q.Set("page", strconv.Itoa(max(page, 1)))
	q.Set("per_page", strconv.Itoa(perPage))
	var resp pexelsResponse
	h := http.Header{"Authorization": {p.Key}}
	if err := getJSON(ctx, p.Client, base+"/v1/search?"+q.Encode(), h, &resp); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(resp.Photos))
	for _, ph := range resp.Photos {
		out = append(out, Candidate{
			Provider:  p.Name(),
			ID:        strconv.FormatInt(ph.ID, 10),
			Title:     ph.Alt,
			Creator:   ph.Photographer,
			License:   "Pexels",
			SourceURL: ph.URL,
			ImageURL:  ph.Src.Large2x,
			ThumbURL:  ph.Src.Medium,
			Width:     ph.Width,
			Height:    ph.Height,
		})
	}
	return out, nil
}
