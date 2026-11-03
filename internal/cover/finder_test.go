package cover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeKeyworder struct {
	kw    string
	err   error
	delay time.Duration
	calls int
}

func (f *fakeKeyworder) Keywords(ctx context.Context, _ string) (string, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return f.kw, f.err
}

func TestFinderQuery(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name, event, keywords string
		kw                    *fakeKeyworder
		want                  string
		wantCalls             int
	}{
		{"explicit keywords win", "เตะบอลวันศุกร์", " futsal ", &fakeKeyworder{kw: "football"}, "futsal", 0},
		{"english name is used as is", "Friday football", "", &fakeKeyworder{kw: "x"}, "Friday football", 0},
		{"thai name is translated", "เตะบอลวันศุกร์", "", &fakeKeyworder{kw: "football friday"}, "football friday", 1},
		{"error falls back to the name", "เตะบอลวันศุกร์", "", &fakeKeyworder{err: errors.New("HTTP 500")}, "เตะบอลวันศุกร์", 1},
		{"rejection falls back to the name", "เตะบอล", "", &fakeKeyworder{err: fmt.Errorf("%w (HTTP 403)", ErrRejected)}, "เตะบอล", 1},
		{"disabled falls back to the name", "เตะบอล", "", &fakeKeyworder{err: ErrDisabled}, "เตะบอล", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Finder{Keywords: tt.kw}
			if got := f.Query(ctx, tt.event, tt.keywords); got != tt.want {
				t.Fatalf("Query = %q, want %q", got, tt.want)
			}
			if tt.kw.calls != tt.wantCalls {
				t.Fatalf("Gemini calls = %d, want %d", tt.kw.calls, tt.wantCalls)
			}
		})
	}
	if got := (&Finder{}).Query(ctx, "เตะบอล", ""); got != "เตะบอล" {
		t.Fatalf("no keyworder: %q", got)
	}
}

func TestFinderQuery_TimesOutFast(t *testing.T) {
	kw := &fakeKeyworder{kw: "late", delay: time.Minute}
	start := time.Now()
	got := (&Finder{Keywords: kw}).Query(context.Background(), "เตะบอล", "")
	if got != "เตะบอล" {
		t.Fatalf("Query = %q, want the name", got)
	}
	if d := time.Since(start); d > KeywordTimeout+2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestCleanKeywords(t *testing.T) {
	for in, want := range map[string]string{
		"football friday night":                 "football friday night",
		"Football, Friday, Night!\n":            "football friday night",
		"  soccer  ":                            "soccer",
		"a b c d e f":                           "a b c d",
		"ฟุตบอล":                                "",
		"**Keywords:** board-games friends 5v5": "keywords board-games friends 5v5",
	} {
		if got := CleanKeywords(in); got != want {
			t.Errorf("CleanKeywords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGemini_RequestAndReply(t *testing.T) {
	var gotPath string
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Contents) == 1 && len(req.Contents[0].Parts) == 1 {
			gotPrompt = req.Contents[0].Parts[0].Text
		}
		fmt.Fprint(w, `{"candidates": [{"content": {"role": "model", "parts": [{"text": "Football, friday night\n"}]}}]}`)
	}))
	defer srv.Close()

	g := &Gemini{Client: srv.Client(), Project: "proj", Endpoint: srv.URL}
	got, err := g.Keywords(context.Background(), "เตะบอลวันศุกร์")
	if err != nil {
		t.Fatal(err)
	}
	if got != "football friday night" {
		t.Fatalf("Keywords = %q", got)
	}
	if want := "/v1/projects/proj/locations/global/publishers/google/models/gemini-2.5-flash-lite:generateContent"; gotPath != want {
		t.Fatalf("path %q, want %q", gotPath, want)
	}
	if !strings.Contains(gotPrompt, "เตะบอลวันศุกร์") || !strings.Contains(gotPrompt, "English keywords") {
		t.Fatalf("prompt %q", gotPrompt)
	}
}

func TestGemini_URL(t *testing.T) {
	g := &Gemini{Project: "p", Location: "us-central1", Model: "gemini-2.5-flash"}
	if want := "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"; g.url() != want {
		t.Fatalf("url %q", g.url())
	}
	if !strings.HasPrefix((&Gemini{Project: "p"}).url(), "https://aiplatform.googleapis.com/v1/projects/p/locations/global/") {
		t.Fatal("global endpoint")
	}
}

func TestGemini_RejectedOnceThenDisabled(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	g := &Gemini{Client: srv.Client(), Project: "p", Endpoint: srv.URL}
	if _, err := g.Keywords(context.Background(), "x"); !errors.Is(err, ErrRejected) {
		t.Fatalf("first call: %v", err)
	}
	if _, err := g.Keywords(context.Background(), "x"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("second call: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
}

func TestGemini_ServerErrorIsNotPermanent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	g := &Gemini{Client: srv.Client(), Project: "p", Endpoint: srv.URL}
	for i := 0; i < 2; i++ {
		if _, err := g.Keywords(context.Background(), "x"); err == nil || errors.Is(err, ErrRejected) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls = %d, want 2", calls.Load())
	}
}

type trackingProvider struct {
	fakeProvider
	tracked []string
}

func (p *trackingProvider) TrackUse(_ context.Context, c Candidate) error {
	p.tracked = append(p.tracked, c.ID)
	return nil
}

func imageServer(t *testing.T) *httptest.Server {
	img := pngOf(t, 100, 40)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(img)
		case "/page.html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html></html>")
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestFinderAuto_SkipsBrokenPicturesAndTracksUse(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()
	p := &trackingProvider{fakeProvider: fakeProvider{name: "unsplash", cands: []Candidate{
		{Provider: "unsplash", ID: "gone", ImageURL: srv.URL + "/missing.jpg", ThumbURL: "t", Width: 2000, Height: 1000},
		{Provider: "unsplash", ID: "html", ImageURL: srv.URL + "/page.html", ThumbURL: "t", Width: 2000, Height: 1000},
		{Provider: "unsplash", ID: "ok", ImageURL: srv.URL + "/good.png", ThumbURL: "t", Width: 2000, Height: 1000,
			Creator: "Kim", License: "Unsplash", SourceURL: "https://unsplash/ok"},
	}}}
	f := &Finder{Providers: Chain{p}, Client: srv.Client()}
	cov, err := f.Auto(context.Background(), "football")
	if err != nil {
		t.Fatal(err)
	}
	checkCover(t, cov.JPEG)
	if cov.Credit != "📷 Kim · Unsplash · https://unsplash/ok" {
		t.Fatalf("credit %q", cov.Credit)
	}
	if len(p.tracked) != 1 || p.tracked[0] != "ok" {
		t.Fatalf("tracked %v, want only the used picture", p.tracked)
	}
}

func TestFinderAuto_NothingFound(t *testing.T) {
	f := &Finder{Providers: Chain{&fakeProvider{name: "openverse"}}}
	if _, err := f.Auto(context.Background(), "x"); !errors.Is(err, ErrNoCover) {
		t.Fatalf("error = %v", err)
	}
	f = &Finder{Providers: Chain{&fakeProvider{name: "openverse", err: errors.New("timeout")}}}
	if _, err := f.Auto(context.Background(), "x"); !errors.Is(err, ErrNoCover) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinderFromURL(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()
	f := &Finder{Client: srv.Client()}
	cov, err := f.FromURL(context.Background(), srv.URL+"/good.png")
	if err != nil {
		t.Fatal(err)
	}
	checkCover(t, cov.JPEG)
	if cov.Credit != "" {
		t.Fatalf("an attachment has no credit, got %q", cov.Credit)
	}
	if _, err := f.FromURL(context.Background(), srv.URL+"/page.html"); !errors.Is(err, ErrNotImage) {
		t.Fatalf("html: %v", err)
	}
}

func TestFinderSearch_CapsResults(t *testing.T) {
	var cands []Candidate
	for i := 0; i < 8; i++ {
		cands = append(cands, cand("openverse", fmt.Sprint(i), 2000, 1000))
	}
	f := &Finder{Providers: Chain{&fakeProvider{name: "openverse", cands: cands}}}
	got, err := f.Search(context.Background(), "q", 1, 5)
	if err != nil || len(got) != 5 {
		t.Fatalf("got %d, %v", len(got), err)
	}
}
