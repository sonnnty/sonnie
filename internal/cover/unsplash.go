package cover

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Unsplash searches unsplash.com (UNSPLASH_ACCESS_KEY). Its API guidelines
// require calling the photo's download_location when a photo is used, and
// crediting the photographer with links carrying utm parameters.
type Unsplash struct {
	Key     string
	BaseURL string // default https://api.unsplash.com
	Client  *http.Client
}

const unsplashUTM = "utm_source=sonnie&utm_medium=referral"

func (u *Unsplash) Name() string { return "unsplash" }

func (u *Unsplash) header() http.Header {
	return http.Header{
		"Authorization":  {"Client-ID " + u.Key},
		"Accept-Version": {"v1"},
	}
}

type unsplashResponse struct {
	Results []struct {
		ID             string `json:"id"`
		Width          int    `json:"width"`
		Height         int    `json:"height"`
		Description    string `json:"description"`
		AltDescription string `json:"alt_description"`
		URLs           struct {
			Regular string `json:"regular"`
			Small   string `json:"small"`
		} `json:"urls"`
		Links struct {
			HTML             string `json:"html"`
			DownloadLocation string `json:"download_location"`
		} `json:"links"`
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	} `json:"results"`
}

func (u *Unsplash) Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error) {
	base := u.BaseURL
	if base == "" {
		base = "https://api.unsplash.com"
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("orientation", "landscape")
	q.Set("content_filter", "high")
	q.Set("page", strconv.Itoa(max(page, 1)))
	q.Set("per_page", strconv.Itoa(perPage))
	var resp unsplashResponse
	if err := getJSON(ctx, u.Client, base+"/search/photos?"+q.Encode(), u.header(), &resp); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		title := r.Description
		if title == "" {
			title = r.AltDescription
		}
		out = append(out, Candidate{
			Provider:         u.Name(),
			ID:               r.ID,
			Title:            title,
			Creator:          r.User.Name,
			License:          "Unsplash",
			SourceURL:        withUTM(r.Links.HTML),
			ImageURL:         r.URLs.Regular,
			ThumbURL:         r.URLs.Small,
			Width:            r.Width,
			Height:           r.Height,
			DownloadLocation: r.Links.DownloadLocation,
		})
	}
	return out, nil
}

// TrackUse calls the photo's download_location, as Unsplash requires when a
// photo is used.
func (u *Unsplash) TrackUse(ctx context.Context, c Candidate) error {
	if c.DownloadLocation == "" {
		return fmt.Errorf("unsplash: photo %s has no download_location", c.ID)
	}
	return getJSON(ctx, u.Client, c.DownloadLocation, u.header(), nil)
}

func withUTM(link string) string {
	if link == "" {
		return ""
	}
	if strings.Contains(link, "?") {
		return link + "&" + unsplashUTM
	}
	return link + "?" + unsplashUTM
}
