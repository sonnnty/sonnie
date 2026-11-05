package cover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeNotifier struct {
	mu    sync.Mutex
	until []time.Time
}

func (n *fakeNotifier) BraveQuotaExhausted(until time.Time) {
	n.mu.Lock()
	n.until = append(n.until, until)
	n.mu.Unlock()
}

func (n *fakeNotifier) calls() []time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]time.Time(nil), n.until...)
}

// quotaServer answers every request with status and headers until ok is
// set, then with one picture.
type quotaServer struct {
	*httptest.Server
	status  atomic.Int32
	hits    atomic.Int32
	headers http.Header
}

func newQuotaServer(t *testing.T, status int, headers map[string]string) *quotaServer {
	qs := &quotaServer{headers: http.Header{}}
	for k, v := range headers {
		qs.headers.Set(k, v)
	}
	qs.status.Store(int32(status))
	qs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qs.hits.Add(1)
		if st := int(qs.status.Load()); st != http.StatusOK {
			for k, v := range qs.headers {
				w.Header()[k] = v
			}
			w.WriteHeader(st)
			fmt.Fprint(w, `{"error":"quota"}`)
			return
		}
		fmt.Fprint(w, braveJSON(braveResult("Brave pic", "https://a.example.com/p", "a.example.com", "https://a.example.com/b.jpg", 1600, 900)))
	}))
	t.Cleanup(qs.Close)
	return qs
}

func newTestBrave(qs *quotaServer, clock *fakeClock, n QuotaNotifier) *Brave {
	b := &Brave{Key: "bk", BaseURL: qs.URL, Client: qs.Client(), Notify: n}
	if clock != nil {
		b.Now = clock.Now
	}
	return b
}

// Brave's real header shape: per-second value first, monthly second.
func monthHeaders(remaining, reset string) map[string]string {
	return map[string]string{
		"X-RateLimit-Limit":     "1, 15000",
		"X-RateLimit-Policy":    "1;w=1, 15000;w=2592000",
		"X-RateLimit-Remaining": remaining,
		"X-RateLimit-Reset":     reset,
	}
}

func TestQuotaExhausted(t *testing.T) {
	endOfMonth := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		exhausted bool
		until     time.Time
	}{
		{"402 with reset", 402, monthHeaders("0, 0", "1, 86400"), true, t0.Add(86400 * time.Second)},
		{"402 without headers", 402, nil, true, endOfMonth},
		{"402 with garbage reset", 402, map[string]string{"X-RateLimit-Reset": "soon"}, true, endOfMonth},
		{"429 monthly used up", 429, monthHeaders("0, 0", "1, 1419704"), true, t0.Add(1419704 * time.Second)},
		{"429 monthly used up, per-second ok", 429, monthHeaders("1, 0", "1, 1419704"), true, t0.Add(1419704 * time.Second)},
		{"429 per-second only", 429, monthHeaders("0, 14990", "1, 1419704"), false, time.Time{}},
		{"429 no headers", 429, nil, false, time.Time{}},
		{"429 malformed remaining", 429, monthHeaders("zero, x", "1, 5"), false, time.Time{}},
		{"429 monthly used up, no reset", 429, map[string]string{
			"X-RateLimit-Policy": "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining": "0, 0"}, true, endOfMonth},
		{"429 reset beyond a month is not believed", 429, monthHeaders("0, 0", "1, 99999999"), true, endOfMonth},
		{"429 monthly without policy: last value", 429, map[string]string{
			"X-RateLimit-Remaining": "1, 0", "X-RateLimit-Reset": "1, 3000"}, true, t0.Add(3000 * time.Second)},
		{"429 single value, long reset", 429, map[string]string{
			"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "500000"}, true, t0.Add(500000 * time.Second)},
		{"429 single value, short reset is per-second", 429, map[string]string{
			"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1"}, false, time.Time{}},
		{"500 is not quota", 500, monthHeaders("0, 0", "1, 5"), false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{Status: tt.status, Header: http.Header{}}
			for k, v := range tt.headers {
				err.Header.Set(k, v)
			}
			until, ex := quotaExhausted(fmt.Errorf("wrapped: %w", err), t0)
			if ex != tt.exhausted || !until.Equal(tt.until) {
				t.Fatalf("got (%v, %v), want (%v, %v)", until, ex, tt.until, tt.exhausted)
			}
		})
	}
	if _, ex := quotaExhausted(errors.New("dial tcp: refused"), t0); ex {
		t.Error("a network error is not quota exhaustion")
	}
}

func TestNextMonth(t *testing.T) {
	for in, want := range map[time.Time]time.Time{
		time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC): time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 5, 0, 0, 0, time.UTC):   time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC):     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got := nextMonth(in); !got.Equal(want) {
			t.Errorf("nextMonth(%v) = %v, want %v", in, got, want)
		}
	}
	// In a time zone ahead of UTC the month is still UTC's.
	if got := nextMonth(time.Date(2026, 11, 30, 20, 0, 0, 0, time.FixedZone("x", 7*3600))); !got.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("nextMonth in +07:00 = %v", got)
	}
}

func TestBrave_402PausesAlertsOnceAndChainFallsBack(t *testing.T) {
	qs := newQuotaServer(t, 402, monthHeaders("0, 0", "1, 86400"))
	clock := &fakeClock{t: t0}
	n := &fakeNotifier{}
	brave := newTestBrave(qs, clock, n)
	unsplash := &fakeProvider{name: "unsplash", cands: []Candidate{cand("unsplash", "u1", 1600, 900)}}
	chain := Chain{brave, unsplash}

	for i := 0; i < 3; i++ {
		got, err := chain.Search(context.Background(), "q", 1, 5)
		if err != nil || len(got) != 1 || got[0].Provider != "unsplash" {
			t.Fatalf("search %d: %v, %v", i, got, err)
		}
	}
	if hits := qs.hits.Load(); hits != 1 {
		t.Errorf("Brave was called %d times; paused after the first 402 it must not be called", hits)
	}
	want := t0.Add(86400 * time.Second)
	if calls := n.calls(); len(calls) != 1 || !calls[0].Equal(want) {
		t.Errorf("notifier calls %v, want one for %v", calls, want)
	}
	if unsplash.calls != 3 {
		t.Errorf("unsplash asked %d times, want 3", unsplash.calls)
	}
}

func TestBrave_PausedSkipsWithoutCallAndResumesAfterReset(t *testing.T) {
	qs := newQuotaServer(t, 402, monthHeaders("0, 0", "1, 3600"))
	clock := &fakeClock{t: t0}
	n := &fakeNotifier{}
	b := newTestBrave(qs, clock, n)
	ctx := context.Background()

	if _, err := b.Search(ctx, "q", 1, 5); err == nil || errors.Is(err, ErrPaused) {
		t.Fatalf("first search: want the HTTP error, got %v", err)
	}
	clock.Advance(3599 * time.Second)
	if _, err := b.Search(ctx, "q", 1, 5); !errors.Is(err, ErrPaused) {
		t.Fatalf("one second before the reset: %v", err)
	}
	if qs.hits.Load() != 1 {
		t.Fatalf("hits %d", qs.hits.Load())
	}

	// The quota is back at the reset time.
	qs.status.Store(200)
	clock.Advance(time.Second)
	got, err := b.Search(ctx, "q", 1, 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("after reset: %v, %v", got, err)
	}
	if qs.hits.Load() != 2 || len(n.calls()) != 1 {
		t.Errorf("hits %d, alerts %d", qs.hits.Load(), len(n.calls()))
	}

	// Exhausted again later: a second pause and a second alert.
	qs.status.Store(402)
	clock.Advance(time.Hour)
	b.Search(ctx, "q", 1, 5)
	if _, err := b.Search(ctx, "q", 1, 5); !errors.Is(err, ErrPaused) {
		t.Fatalf("second pause: %v", err)
	}
	if len(n.calls()) != 2 {
		t.Errorf("alerts %d, want 2 (one per pause)", len(n.calls()))
	}
}

func TestBrave_402WithoutResetPausesToNextMonth(t *testing.T) {
	qs := newQuotaServer(t, 402, nil)
	clock := &fakeClock{t: time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC)}
	n := &fakeNotifier{}
	b := newTestBrave(qs, clock, n)
	b.Search(context.Background(), "q", 1, 5)
	if calls := n.calls(); len(calls) != 1 || !calls[0].Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("alerts %v", calls)
	}
	clock.Advance(time.Hour)
	qs.status.Store(200)
	if got, err := b.Search(context.Background(), "q", 1, 5); err != nil || len(got) != 1 {
		t.Fatalf("on the 1st: %v, %v", got, err)
	}
}

func TestBrave_Monthly429PausesButPerSecond429DoesNot(t *testing.T) {
	// Per-second 429: an ordinary error, no pause, no alert; the next request calls Brave again.
	qs := newQuotaServer(t, 429, monthHeaders("0, 14000", "1, 1419704"))
	n := &fakeNotifier{}
	b := newTestBrave(qs, &fakeClock{t: t0}, n)
	unsplash := &fakeProvider{name: "unsplash", cands: []Candidate{cand("unsplash", "u1", 1600, 900)}}
	chain := Chain{b, unsplash}
	for i := 0; i < 2; i++ {
		got, err := chain.Search(context.Background(), "q", 1, 5)
		if err != nil || len(got) != 1 || got[0].Provider != "unsplash" {
			t.Fatalf("per-second 429, search %d: %v, %v", i, got, err)
		}
	}
	if qs.hits.Load() != 2 || len(n.calls()) != 0 {
		t.Errorf("per-second 429: hits %d, alerts %d; want 2 and 0", qs.hits.Load(), len(n.calls()))
	}

	// Monthly 429: paused from the first one.
	qs2 := newQuotaServer(t, 429, monthHeaders("0, 0", "1, 1419704"))
	n2 := &fakeNotifier{}
	chain = Chain{newTestBrave(qs2, &fakeClock{t: t0}, n2), unsplash}
	for i := 0; i < 2; i++ {
		if got, err := chain.Search(context.Background(), "q", 1, 5); err != nil || got[0].Provider != "unsplash" {
			t.Fatalf("monthly 429, search %d: %v, %v", i, got, err)
		}
	}
	if calls := n2.calls(); qs2.hits.Load() != 1 || len(calls) != 1 || !calls[0].Equal(t0.Add(1419704*time.Second)) {
		t.Errorf("monthly 429: hits %d, alerts %v", qs2.hits.Load(), calls)
	}
}

func TestBrave_NilNotifierAndConcurrentFailuresAlertOnce(t *testing.T) {
	qs := newQuotaServer(t, 402, nil)
	newTestBrave(qs, &fakeClock{t: t0}, nil).Search(context.Background(), "q", 1, 5) // no notifier: no panic

	n := &fakeNotifier{}
	b := newTestBrave(qs, &fakeClock{t: t0}, n)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Search(context.Background(), "q", 1, 5) }()
	}
	wg.Wait()
	if len(n.calls()) != 1 {
		t.Errorf("alerts %d, want 1 even with requests in flight together", len(n.calls()))
	}
}

func TestChain_PausedProviderIsSkippedQuietly(t *testing.T) {
	paused := &fakeProvider{name: "brave", err: ErrPaused}
	only := Chain{paused}
	if got, err := only.Search(context.Background(), "q", 1, 5); got != nil || err != nil {
		t.Errorf("only a paused provider: %v, %v", got, err)
	}
}

func TestChain_StatusShowsBravePause(t *testing.T) {
	qs := newQuotaServer(t, 402, monthHeaders("0, 0", "1, 3600"))
	clock := &fakeClock{t: t0}
	b := newTestBrave(qs, clock, nil)
	c := Chain{b, &Openverse{}}

	st := c.Status()
	if len(st) != 2 || st[0].Name != "brave" || st[1].Name != "openverse" || !st[0].PausedUntil.IsZero() {
		t.Fatalf("before the pause: %+v", st)
	}
	b.Search(context.Background(), "q", 1, 5)
	st = c.Status()
	if want := t0.Add(time.Hour); !st[0].PausedUntil.Equal(want) || !st[1].PausedUntil.IsZero() {
		t.Fatalf("paused status %+v, want brave until %s", st, want)
	}
}
