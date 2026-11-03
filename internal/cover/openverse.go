package cover

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Openverse searches openverse.org, a Creative Commons image index that
// needs no key. Each picture has its own licence; only those that allow
// reuse with attribution are kept.
type Openverse struct {
	BaseURL string // default https://api.openverse.org
	Client  *http.Client
}

// reusableLicenses are the Openverse licence codes that allow commercial
// reuse and changes (cropping): no NC, no ND.
var reusableLicenses = map[string]bool{"cc0": true, "pdm": true, "by": true, "by-sa": true}

// ReusableLicense reports whether an Openverse licence code may be used for
// a cover.
func ReusableLicense(code string) bool {
	return reusableLicenses[strings.ToLower(code)]
}

// downloadableTypes are the file types Process can read; Openverse also
// returns GIF and SVG.
var downloadableTypes = map[string]bool{"": true, "jpg": true, "jpeg": true, "png": true, "webp": true}

func (o *Openverse) Name() string { return "openverse" }

type openverseResponse struct {
	Results []struct {
		ID                string `json:"id"`
		Title             string `json:"title"`
		URL               string `json:"url"`
		Thumbnail         string `json:"thumbnail"`
		Creator           string `json:"creator"`
		License           string `json:"license"`
		LicenseVersion    string `json:"license_version"`
		ForeignLandingURL string `json:"foreign_landing_url"`
		Filetype          string `json:"filetype"`
		Width             int    `json:"width"`
		Height            int    `json:"height"`
		Mature            bool   `json:"mature"`
	} `json:"results"`
}

func (o *Openverse) Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error) {
	base := o.BaseURL
	if base == "" {
		base = "https://api.openverse.org"
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("license", "cc0,pdm,by,by-sa")
	q.Set("mature", "false")
	q.Set("page", strconv.Itoa(max(page, 1)))
	q.Set("page_size", strconv.Itoa(perPage))
	var resp openverseResponse
	if err := getJSON(ctx, o.Client, base+"/v1/images/?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, r := range resp.Results {
		// The licence filter is also applied here in case the API ignores it.
		if !ReusableLicense(r.License) || r.Mature || !downloadableTypes[strings.ToLower(r.Filetype)] {
			continue
		}
		out = append(out, Candidate{
			Provider:  o.Name(),
			ID:        r.ID,
			Title:     r.Title,
			Creator:   r.Creator,
			License:   licenseName(r.License, r.LicenseVersion),
			SourceURL: r.ForeignLandingURL,
			ImageURL:  r.URL,
			ThumbURL:  r.Thumbnail,
			Width:     r.Width,
			Height:    r.Height,
		})
	}
	return out, nil
}

func licenseName(code, version string) string {
	switch strings.ToLower(code) {
	case "pdm":
		return "Public Domain"
	case "cc0":
		return "CC0"
	}
	name := "CC " + strings.ToUpper(code)
	if version != "" {
		name += " " + version
	}
	return name
}
