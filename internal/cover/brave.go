package cover

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Brave searches the web for pictures with the Brave Search API
// (BRAVE_SEARCH_API_KEY). Unlike the other providers its results are
// ordinary web images with no licence; the owner of a private server turns
// it on by setting the key and off by unsetting it.
type Brave struct {
	Key     string
	BaseURL string // default https://api.search.brave.com
	Client  *http.Client
}

// braveCount is how many results are asked for; a page of the picker is a
// slice of the ones that survive the filters, as the API has no paging.
const braveCount = 20

// stockDomains are stock photo sites whose results carry watermarks. A
// result is dropped when its page, its source or its image is on one of
// them or on a subdomain. This is the one list to edit.
var stockDomains = []string{
	"gettyimages.com", "gettyimages.co.th", "istockphoto.com", "shutterstock.com",
	"alamy.com", "dreamstime.com", "123rf.com", "depositphotos.com",
	"stock.adobe.com", "adobestock.com", "ftcdn.net", // Adobe Stock
	"vecteezy.com", "freepik.com", "magnific.com", "pond5.com",
	"canstockphoto.com", "bigstockphoto.com", "bigstock.com", "agefotostock.com",
}

// braveMinRatio is the least width/height of a result whose size is known;
// covers are 5:2 so tall pictures lose too much.
const braveMinRatio = 1.2

func (b *Brave) Name() string { return "brave" }

type braveResponse struct {
	Results []struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		Source     string `json:"source"`
		Properties struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"properties"`
		Thumbnail struct {
			Src string `json:"src"`
		} `json:"thumbnail"`
	} `json:"results"`
}

func (b *Brave) Search(ctx context.Context, query string, page, perPage int) ([]Candidate, error) {
	base := b.BaseURL
	if base == "" {
		base = "https://api.search.brave.com"
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("count", strconv.Itoa(braveCount))
	q.Set("safesearch", "strict")
	header := http.Header{"X-Subscription-Token": {b.Key}}
	var resp braveResponse
	if err := getJSON(ctx, b.Client, base+"/res/v1/images/search?"+q.Encode(), header, &resp); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, r := range resp.Results {
		img := r.Properties.URL
		w, h := r.Properties.Width, r.Properties.Height
		if img == "" || r.Thumbnail.Src == "" || stockDomain(r.URL, r.Source, img) || !coverImageURL(img) {
			continue
		}
		if w > 0 && h > 0 && (w < Width || float64(w)/float64(h) < braveMinRatio) {
			continue
		}
		out = append(out, Candidate{
			Provider:  b.Name(),
			ID:        img,
			Title:     r.Title,
			SourceURL: r.URL,
			Source:    sourceDomain(r.Source, r.URL),
			ImageURL:  img,
			ThumbURL:  r.Thumbnail.Src,
			Width:     w,
			Height:    h,
		})
	}
	// https pictures first; the order is otherwise Brave's.
	sort.SliceStable(out, func(i, j int) bool {
		return strings.HasPrefix(out[i].ImageURL, "https://") && !strings.HasPrefix(out[j].ImageURL, "https://")
	})
	start := (max(page, 1) - 1) * perPage
	if start >= len(out) {
		return nil, nil
	}
	return out[start:min(start+perPage, len(out))], nil
}

// stockDomain reports whether any of the given URLs or domains belongs to
// a stock photo site.
func stockDomain(refs ...string) bool {
	for _, ref := range refs {
		host := hostOf(ref)
		for _, d := range stockDomains {
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		}
	}
	return false
}

// hostOf is the lower-case host of a URL, or of a bare domain.
func hostOf(ref string) string {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, "://") {
		ref = "https://" + ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// sourceDomain is the domain shown in the credit: Brave's source, else the
// page's host.
func sourceDomain(source, page string) string {
	if h := hostOf(source); h != "" && strings.TrimSpace(source) != "" {
		return h
	}
	return hostOf(page)
}

// coverImageURL accepts http(s) URLs whose file extension, if there is one,
// is JPEG, PNG or WebP. Many image URLs have no extension; the download
// checks the real type.
func coverImageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case "", ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}
