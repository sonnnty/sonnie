package cover

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func braveResult(title, page, source, img string, w, h int) string {
	return fmt.Sprintf(`{"title": %q, "url": %q, "source": %q,
		"properties": {"url": %q, "width": %d, "height": %d},
		"thumbnail": {"src": "https://imgs.search.brave.com/thumb/%s"}}`, title, page, source, img, w, h, source)
}

func braveJSON(results ...string) string {
	return `{"type": "images", "results": [` + strings.Join(results, ",") + `]}`
}

func braveServer(t *testing.T, body string, gotReq *http.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotReq != nil {
			*gotReq = *r
		}
		if r.Header.Get("X-Subscription-Token") != "bk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, body)
	}))
}

func TestBrave_RequestAndParse(t *testing.T) {
	var req http.Request
	srv := braveServer(t, braveJSON(
		braveResult("Football night", "https://sports.example.com/a", "sports.example.com", "https://cdn.example.com/a.jpg", 1600, 900),
	), &req)
	defer srv.Close()

	got, err := (&Brave{Key: "bk", BaseURL: srv.URL, Client: srv.Client()}).Search(context.Background(), "football night", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/res/v1/images/search" {
		t.Errorf("path %q", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("q") != "football night" || q.Get("count") != "20" || q.Get("safesearch") != "strict" {
		t.Errorf("query %q", req.URL.RawQuery)
	}
	if req.Header.Get("X-Subscription-Token") != "bk" || req.Header.Get("Accept") != "application/json" {
		t.Errorf("headers %v", req.Header)
	}
	want := Candidate{Provider: "brave", ID: "https://cdn.example.com/a.jpg", Title: "Football night",
		SourceURL: "https://sports.example.com/a", Source: "sports.example.com",
		ImageURL: "https://cdn.example.com/a.jpg", ThumbURL: "https://imgs.search.brave.com/thumb/sports.example.com",
		Width: 1600, Height: 900}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v", got)
	}
}

func TestBrave_RejectedKey(t *testing.T) {
	srv := braveServer(t, braveJSON(), nil)
	defer srv.Close()
	if _, err := (&Brave{Key: "wrong", BaseURL: srv.URL, Client: srv.Client()}).Search(context.Background(), "x", 1, 5); err == nil {
		t.Fatal("want an error for a rejected key")
	}
}

func TestBrave_Filters(t *testing.T) {
	srv := braveServer(t, braveJSON(
		braveResult("getty page", "https://www.gettyimages.com/x", "gettyimages.com", "https://media.gettyimages.com/x.jpg", 1600, 900),
		braveResult("sub-domain", "https://de.depositphotos.com/y", "de.depositphotos.com", "https://cdn.example.com/y.jpg", 1600, 900),
		braveResult("stock image host", "https://blog.example.com/z", "blog.example.com", "https://as1.ftcdn.net/z.jpg", 1600, 900),
		braveResult("stock page, neutral source", "https://www.shutterstock.com/w", "example.org", "https://cdn.example.com/w.jpg", 1600, 900),
		braveResult("narrow", "https://a.example.com/1", "a.example.com", "https://a.example.com/1.jpg", 740, 400),
		braveResult("portrait", "https://a.example.com/2", "a.example.com", "https://a.example.com/2.jpg", 1000, 1500),
		braveResult("squarish", "https://a.example.com/3", "a.example.com", "https://a.example.com/3.jpg", 1000, 900),
		braveResult("gif", "https://a.example.com/4", "a.example.com", "https://a.example.com/4.gif", 1600, 900),
		braveResult("svg", "https://a.example.com/5", "a.example.com", "https://a.example.com/5.svg?x=1", 1600, 900),
		braveResult("ftp", "https://a.example.com/6", "a.example.com", "ftp://a.example.com/6.jpg", 1600, 900),
		braveResult("lookalike domain", "https://notgettyimages.com/7", "notgettyimages.com", "https://notgettyimages.com/7.jpg", 1600, 900),
		braveResult("png", "https://b.example.com/8", "b.example.com", "https://b.example.com/8.PNG", 1600, 900),
		braveResult("no extension", "https://b.example.com/9", "b.example.com", "https://b.example.com/image?id=9", 1200, 800),
		braveResult("unknown size", "https://b.example.com/10", "b.example.com", "https://b.example.com/10.webp", 0, 0),
		`{"title": "no thumbnail", "url": "https://c.example.com", "source": "c.example.com", "properties": {"url": "https://c.example.com/1.jpg", "width": 1600, "height": 900}}`,
	), nil)
	defer srv.Close()

	got, err := (&Brave{Key: "bk", BaseURL: srv.URL, Client: srv.Client()}).Search(context.Background(), "q", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, c := range got {
		kept = append(kept, c.Title)
	}
	want := "lookalike domain,png,no extension,unknown size"
	if strings.Join(kept, ",") != want {
		t.Fatalf("kept %v, want %s", kept, want)
	}
}

func TestStockDomains(t *testing.T) {
	for _, d := range []string{"gettyimages", "istockphoto", "shutterstock", "alamy", "dreamstime", "123rf",
		"depositphotos", "adobe", "ftcdn", "vecteezy", "freepik", "magnific", "pond5", "canstockphoto", "bigstock", "agefotostock"} {
		found := false
		for _, s := range stockDomains {
			found = found || strings.Contains(s, d)
		}
		if !found {
			t.Errorf("stockDomains lacks %s", d)
		}
	}
	if !stockDomain("https://image.freepik.com/a.jpg") || !stockDomain("", "stock.adobe.com") || stockDomain("https://example.com/freepik.com") {
		t.Error("stockDomain matching")
	}
}

func TestBrave_HTTPSFirstAndPaging(t *testing.T) {
	var rs []string
	rs = append(rs, braveResult("http", "https://a.example.com/0", "a.example.com", "http://a.example.com/0.jpg", 1600, 900))
	for i := 1; i <= 4; i++ {
		rs = append(rs, braveResult(fmt.Sprint("s", i), "https://a.example.com/p", "a.example.com", fmt.Sprintf("https://a.example.com/%d.jpg", i), 1600, 900))
	}
	srv := braveServer(t, braveJSON(rs...), nil)
	defer srv.Close()
	b := &Brave{Key: "bk", BaseURL: srv.URL, Client: srv.Client()}
	p1, _ := b.Search(context.Background(), "q", 1, 3)
	p2, _ := b.Search(context.Background(), "q", 2, 3)
	p3, _ := b.Search(context.Background(), "q", 3, 3)
	if ids3 := ids(p1); ids3 != "https://a.example.com/1.jpg,https://a.example.com/2.jpg,https://a.example.com/3.jpg" {
		t.Errorf("page 1: %s", ids3)
	}
	if ids2 := ids(p2); ids2 != "https://a.example.com/4.jpg,http://a.example.com/0.jpg" {
		t.Errorf("page 2 (http last): %s", ids2)
	}
	if len(p3) != 0 {
		t.Errorf("page 3: %v", ids(p3))
	}
}

func TestBraveCredit(t *testing.T) {
	c := Candidate{Provider: "brave", Title: "Football night", Source: "sports.example.com", SourceURL: "https://sports.example.com/a", License: "ignored"}
	if got, want := c.Credit(), "📷 sports.example.com · https://sports.example.com/a"; got != want {
		t.Fatalf("Credit() = %q, want %q", got, want)
	}
	if sourceDomain("", "https://www.Example.com/p") != "www.example.com" || sourceDomain("Foo.com", "https://x.org") != "foo.com" {
		t.Fatal("sourceDomain")
	}
}

// End to end through Chain, Finder and a fake image host: the first Brave
// pictures fail to download, so the next result is used, never a thumbnail.
func TestFinderAuto_BraveFallsBackToNextResult(t *testing.T) {
	img := pngOf(t, 100, 40)
	var mu sync.Mutex
	var hits []string
	var imgSrv *httptest.Server
	imgSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(img)
		case "/page.jpg":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer imgSrv.Close()
	apiSrv := braveServer(t, braveJSON(
		braveResult("gone", "https://one.example.com/p", "one.example.com", imgSrv.URL+"/gone.jpg", 1600, 900),
		braveResult("html", "https://two.example.com/p", "two.example.com", imgSrv.URL+"/page.jpg", 1600, 900),
		braveResult("ok", "https://three.example.com/p", "three.example.com", imgSrv.URL+"/ok.png", 1600, 900),
	), nil)
	defer apiSrv.Close()

	f := &Finder{Providers: Chain{&Brave{Key: "bk", BaseURL: apiSrv.URL, Client: apiSrv.Client()}}, Client: imgSrv.Client()}
	cov, err := f.Auto(context.Background(), "football")
	if err != nil {
		t.Fatal(err)
	}
	checkCover(t, cov.JPEG)
	if cov.Credit != "📷 three.example.com · https://three.example.com/p" {
		t.Fatalf("credit %q", cov.Credit)
	}
	if strings.Join(hits, ",") != "/gone.jpg,/page.jpg,/ok.png" {
		t.Fatalf("image requests %v; thumbnails must never be fetched", hits)
	}
}
