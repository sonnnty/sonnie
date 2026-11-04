package cover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const openverseJSON = `{"result_count": 4, "page_count": 1, "page_size": 20, "page": 1, "results": [
 {"id": "nc", "title": "Not commercial", "url": "https://img/nc.jpg", "thumbnail": "https://ov/nc/thumb/",
  "creator": "A", "license": "by-nc", "license_version": "2.0", "foreign_landing_url": "https://flickr/nc",
  "filetype": "jpg", "width": 1600, "height": 900},
 {"id": "nd", "title": "No derivatives", "url": "https://img/nd.jpg", "thumbnail": "https://ov/nd/thumb/",
  "creator": "B", "license": "by-nd", "license_version": "4.0", "foreign_landing_url": "https://flickr/nd",
  "filetype": "jpg", "width": 1600, "height": 900},
 {"id": "small", "title": "Small", "url": "https://img/small.jpg", "thumbnail": "https://ov/small/thumb/",
  "creator": "C", "license": "cc0", "license_version": "1.0", "foreign_landing_url": "https://wiki/small",
  "filetype": "jpg", "width": 640, "height": 480},
 {"id": "gif", "title": "Animated", "url": "https://img/a.gif", "thumbnail": "https://ov/gif/thumb/",
  "creator": "D", "license": "by", "license_version": "2.0", "foreign_landing_url": "https://flickr/gif",
  "filetype": "gif", "width": 1600, "height": 900},
 {"id": "good", "title": "Football match at night", "url": "https://img/good.jpg", "thumbnail": "https://ov/good/thumb/",
  "creator": "Jane Doe", "license": "by-sa", "license_version": "2.0", "foreign_landing_url": "https://flickr/good",
  "filetype": "jpg", "width": 2048, "height": 1365},
 {"id": "pd", "title": "Old stadium", "url": "https://img/pd.png", "thumbnail": "https://ov/pd/thumb/",
  "creator": "", "license": "pdm", "license_version": "", "foreign_landing_url": "https://wiki/pd",
  "filetype": "png", "width": 1200, "height": 2000}
]}`

func TestOpenverse_FiltersLicencesAndParses(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, openverseJSON)
	}))
	defer srv.Close()

	o := &Openverse{BaseURL: srv.URL, Client: srv.Client()}
	got, err := o.Search(context.Background(), "football night", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"q=football+night", "license=cc0%2Cpdm%2Cby%2Cby-sa", "page=2", "page_size=5"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q lacks %q", gotQuery, want)
		}
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "small,good,pd" {
		t.Fatalf("kept %v, want small,good,pd (no NC, ND or GIF)", ids)
	}
	good := got[1]
	if good.License != "CC BY-SA 2.0" || good.Creator != "Jane Doe" || good.SourceURL != "https://flickr/good" ||
		good.ImageURL != "https://img/good.jpg" || good.ThumbURL != "https://ov/good/thumb/" || good.Provider != "openverse" {
		t.Fatalf("parsed %+v", good)
	}
	if got[2].License != "Public Domain" || got[0].License != "CC0" {
		t.Fatalf("licence names: %q, %q", got[2].License, got[0].License)
	}
}

func TestReusableLicense(t *testing.T) {
	for code, want := range map[string]bool{
		"cc0": true, "pdm": true, "by": true, "by-sa": true, "BY-SA": true,
		"by-nc": false, "by-nd": false, "by-nc-sa": false, "by-nc-nd": false, "sampling+": false, "": false,
	} {
		if got := ReusableLicense(code); got != want {
			t.Errorf("ReusableLicense(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestCredit(t *testing.T) {
	c := Candidate{Title: "Football match at night under the floodlights of the stadium", Creator: "Jane Doe",
		License: "CC BY-SA 2.0", SourceURL: "https://flickr/good"}
	got := c.Credit()
	want := "📷 “Football match at night under the flood…” — Jane Doe · CC BY-SA 2.0 · https://flickr/good"
	if got != want {
		t.Fatalf("Credit() =\n%q\nwant\n%q", got, want)
	}
	if got := (Candidate{License: "CC0", SourceURL: "https://x"}).Credit(); got != "📷 CC0 · https://x" {
		t.Fatalf("no title/creator: %q", got)
	}
}

func TestPexels_SendsKeyAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "pk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/search" || r.URL.Query().Get("orientation") != "landscape" || r.URL.Query().Get("query") != "football" {
			t.Errorf("unexpected request %s", r.URL)
		}
		fmt.Fprint(w, `{"photos": [{"id": 123, "width": 4000, "height": 2500, "url": "https://www.pexels.com/photo/123/",
			"alt": "Ball on grass", "photographer": "Sam", "src": {"large2x": "https://p/l2x.jpg", "medium": "https://p/m.jpg"}}]}`)
	}))
	defer srv.Close()

	got, err := (&Pexels{Key: "pk", BaseURL: srv.URL, Client: srv.Client()}).Search(context.Background(), "football", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := Candidate{Provider: "pexels", ID: "123", Title: "Ball on grass", Creator: "Sam", License: "Pexels",
		SourceURL: "https://www.pexels.com/photo/123/", ImageURL: "https://p/l2x.jpg", ThumbURL: "https://p/m.jpg", Width: 4000, Height: 2500}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v", got)
	}
	if _, err := (&Pexels{Key: "wrong", BaseURL: srv.URL, Client: srv.Client()}).Search(context.Background(), "x", 1, 5); err == nil {
		t.Fatal("want an error for a rejected key")
	}
}

func TestUnsplash_ParsesAndTracksDownload(t *testing.T) {
	var mu sync.Mutex
	var tracked []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Client-ID uk" || r.Header.Get("Accept-Version") != "v1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/search/photos":
			fmt.Fprintf(w, `{"results": [{"id": "abc", "width": 5000, "height": 3000, "description": null,
				"alt_description": "people playing football",
				"urls": {"regular": "https://u/reg.jpg", "small": "https://u/small.jpg"},
				"links": {"html": "https://unsplash.com/photos/abc", "download_location": "%s/photos/abc/download?ixid=1"},
				"user": {"name": "Kim"}}]}`, srv.URL)
		case "/photos/abc/download":
			mu.Lock()
			tracked = append(tracked, r.URL.RawQuery)
			mu.Unlock()
			fmt.Fprint(w, `{"url": "https://u/raw.jpg"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	u := &Unsplash{Key: "uk", BaseURL: srv.URL, Client: srv.Client()}
	got, err := u.Search(context.Background(), "football", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	c := got[0]
	if c.Title != "people playing football" || c.Creator != "Kim" || c.ImageURL != "https://u/reg.jpg" ||
		c.ThumbURL != "https://u/small.jpg" || c.SourceURL != "https://unsplash.com/photos/abc?utm_source=sonnie&utm_medium=referral" {
		t.Fatalf("parsed %+v", c)
	}
	if err := u.TrackUse(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0] != "ixid=1" {
		t.Fatalf("download_location calls: %v", tracked)
	}
}

type fakeProvider struct {
	name  string
	cands []Candidate
	err   error
	calls int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Search(_ context.Context, _ string, _, _ int) ([]Candidate, error) {
	f.calls++
	return f.cands, f.err
}

func cand(provider, id string, w, h int) Candidate {
	return Candidate{Provider: provider, ID: id, ImageURL: "https://img/" + id, ThumbURL: "https://thumb/" + id, Width: w, Height: h}
}

func TestNewChain_KeyedProvidersFirst(t *testing.T) {
	tests := []struct {
		pexels, unsplash, brave string
		want                    string
	}{
		{"", "", "", "openverse"},
		{"p", "", "", "pexels,openverse"},
		{"", "u", "", "unsplash,openverse"},
		{"p", "u", "", "pexels,unsplash,openverse"},
		{"", "", "b", "brave,openverse"},
		{"", "u", "b", "unsplash,brave,openverse"},
		{"p", "u", "b", "pexels,unsplash,brave,openverse"},
	}
	for _, tt := range tests {
		if got := strings.Join(NewChain(nil, tt.pexels, tt.unsplash, tt.brave).Names(), ","); got != tt.want {
			t.Errorf("NewChain(%q, %q, %q) = %s, want %s", tt.pexels, tt.unsplash, tt.brave, got, tt.want)
		}
	}
}

func TestChain_FallsBackOnErrorOrNoResults(t *testing.T) {
	failing := &fakeProvider{name: "pexels", err: errors.New("HTTP 429")}
	empty := &fakeProvider{name: "unsplash"}
	ov := &fakeProvider{name: "openverse", cands: []Candidate{cand("openverse", "a", 640, 480), cand("openverse", "b", 1600, 900)}}
	got, err := Chain{failing, empty, ov}.Search(context.Background(), "q", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if failing.calls != 1 || empty.calls != 1 || ov.calls != 1 {
		t.Fatalf("calls: %d %d %d", failing.calls, empty.calls, ov.calls)
	}
	// The landscape ≥ 800 px picture comes first.
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("got %+v", got)
	}
}

func ids(cands []Candidate) string {
	var s []string
	for _, c := range cands {
		s = append(s, c.ID)
	}
	return strings.Join(s, ",")
}

func TestChain_FillsSlotsInProviderOrder(t *testing.T) {
	u := &fakeProvider{name: "unsplash", cands: []Candidate{cand("unsplash", "u1", 2000, 1000), cand("unsplash", "u2", 2000, 1000)}}
	b := &fakeProvider{name: "brave", cands: []Candidate{cand("brave", "b1", 1600, 900), cand("brave", "b2", 1600, 900), cand("brave", "b3", 1600, 900)}}
	ov := &fakeProvider{name: "openverse", cands: []Candidate{cand("openverse", "o1", 2000, 1000), cand("openverse", "o2", 2000, 1000)}}
	got, err := Chain{u, b, ov}.Search(context.Background(), "q", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "u1,u2,b1,b2,b3" {
		t.Fatalf("got %s, want unsplash, then brave to fill the 5 slots", ids(got))
	}
	if ov.calls != 0 {
		t.Fatalf("openverse was asked although the slots were full")
	}
	// Brave short: Openverse fills the rest.
	b.cands = b.cands[:1]
	got, _ = Chain{u, b, ov}.Search(context.Background(), "q", 1, 5)
	if ids(got) != "u1,u2,b1,o1,o2" {
		t.Fatalf("got %s", ids(got))
	}
}

func TestChain_SkipsDuplicateImageURLs(t *testing.T) {
	u := &fakeProvider{name: "unsplash", cands: []Candidate{cand("unsplash", "u1", 2000, 1000)}}
	dup := cand("brave", "b-dup", 1600, 900)
	dup.ImageURL = u.cands[0].ImageURL
	b := &fakeProvider{name: "brave", cands: []Candidate{dup, cand("brave", "b2", 1600, 900), dup}}
	ov := &fakeProvider{name: "openverse", cands: []Candidate{cand("openverse", "o1", 2000, 1000)}}
	got, err := Chain{u, b, ov}.Search(context.Background(), "q", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "u1,b2,o1" {
		t.Fatalf("got %s, want u1,b2,o1", ids(got))
	}
}

func TestChain_PartialFailureKeepsResults(t *testing.T) {
	u := &fakeProvider{name: "unsplash", cands: []Candidate{cand("unsplash", "u1", 2000, 1000)}}
	b := &fakeProvider{name: "brave", err: errors.New("HTTP 429")}
	got, err := Chain{u, b}.Search(context.Background(), "q", 1, 5)
	if err != nil || ids(got) != "u1" {
		t.Fatalf("got %s, %v", ids(got), err)
	}
}

func TestChain_AllFailing(t *testing.T) {
	_, err := Chain{&fakeProvider{name: "a", err: errors.New("down")}, &fakeProvider{name: "b", err: errors.New("down")}}.
		Search(context.Background(), "q", 1, 5)
	if err == nil || !strings.Contains(err.Error(), "a: down") || !strings.Contains(err.Error(), "b: down") {
		t.Fatalf("error = %v", err)
	}
	got, err := Chain{&fakeProvider{name: "a"}}.Search(context.Background(), "q", 1, 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("no results: %v %v", got, err)
	}
}
