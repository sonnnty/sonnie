package cover

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrPaused is returned by a provider that is skipped without being called,
// because its quota is used up until a known time. Chain does not log it.
var ErrPaused = errors.New("cover: provider paused until its quota resets")

// QuotaNotifier hears when Brave's monthly credit runs out. It is called
// once per pause, synchronously from a search, so an implementation that
// talks to the network should hand the work to a goroutine. The Discord
// message lives in the bot, not here.
type QuotaNotifier interface {
	BraveQuotaExhausted(until time.Time)
}

// Brave's rate-limit headers hold one value per window, comma-separated,
// shortest window first, e.g. on a plan with 1 request a second and 15 000 a
// month:
//
//	X-RateLimit-Limit: 1, 15000
//	X-RateLimit-Policy: 1;w=1, 15000;w=2592000
//	X-RateLimit-Remaining: 0, 14990
//	X-RateLimit-Reset: 1, 1419704
//
// Reset is in seconds from now. The parsing below is defensive: any header
// may be missing, have one value or be malformed.

const (
	// monthlyWindow is the least window, in seconds, taken to be a monthly
	// quota rather than a per-second or per-minute limit.
	monthlyWindow = 24 * 60 * 60
	// maxPause caps a reset read from a header; anything longer than a
	// month is not believed and the 1st of next month is used instead.
	maxPause = 32 * 24 * time.Hour
)

// ints parses "1, 15000" into [1 15000]; ok is false when it is empty or
// any part is not a non-negative integer.
func ints(v string) ([]int, bool) {
	if strings.TrimSpace(v) == "" {
		return nil, false
	}
	var out []int
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// windows parses "1;w=1, 15000;w=2592000" into the window length of each
// value (0 where it is not given).
func windows(v string) []int {
	var out []int
	for _, part := range strings.Split(v, ",") {
		w := 0
		for _, f := range strings.Split(part, ";") {
			f = strings.TrimSpace(f)
			if rest, ok := strings.CutPrefix(f, "w="); ok {
				if n, err := strconv.Atoi(rest); err == nil && n > 0 {
					w = n
				}
			}
		}
		out = append(out, w)
	}
	return out
}

// monthlyWindowIndex finds which value of the rate-limit headers is the
// monthly one: the longest window of X-RateLimit-Policy when that gives one
// of at least a day, else, with several values, the last, else (a single
// value) the only one if its reset is more than an hour away. ok is false
// when no value looks monthly.
func monthlyWindowIndex(h http.Header, n int, reset []int) (idx int, ok bool) {
	if pol := windows(h.Get("X-RateLimit-Policy")); len(pol) == n {
		best := -1
		for i, w := range pol {
			if w >= monthlyWindow && (best < 0 || w > pol[best]) {
				best = i
			}
		}
		if best >= 0 {
			return best, true
		}
	}
	if n >= 2 {
		return n - 1, true
	}
	if n == 1 && len(reset) == 1 && reset[0] > 3600 {
		return 0, true
	}
	return 0, false
}

// monthlyQuota reads the rate-limit headers of an answer. exhausted is
// true when the monthly window has no requests left. reset is when it
// refills, relative to now; it is zero when the headers do not say.
func monthlyQuota(h http.Header, now time.Time) (exhausted bool, reset time.Time) {
	remaining, ok := ints(h.Get("X-RateLimit-Remaining"))
	if !ok {
		return false, time.Time{}
	}
	resets, _ := ints(h.Get("X-RateLimit-Reset"))
	idx, ok := monthlyWindowIndex(h, len(remaining), resets)
	if !ok || remaining[idx] != 0 {
		return false, time.Time{}
	}
	if idx < len(resets) && resets[idx] > 0 {
		if d := time.Duration(resets[idx]) * time.Second; d <= maxPause {
			reset = now.Add(d)
		}
	}
	return true, reset
}

// nextMonth is 00:00 UTC on the 1st of the month after now's.
func nextMonth(now time.Time) time.Time {
	y, m, _ := now.UTC().Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
}

// quotaExhausted decides from a failed Brave request whether the monthly
// credit is used up, and until when to pause. HTTP 402 always means so
// (payment required); 429 only when the monthly window has nothing left,
// since a plain per-second 429 clears by itself.
func quotaExhausted(err error, now time.Time) (until time.Time, exhausted bool) {
	var he *HTTPError
	if !errors.As(err, &he) {
		return time.Time{}, false
	}
	switch he.Status {
	case http.StatusPaymentRequired:
		_, reset := monthlyQuota(he.Header, now)
		if reset.IsZero() {
			reset = nextMonth(now)
		}
		return reset, true
	case http.StatusTooManyRequests:
		ex, reset := monthlyQuota(he.Header, now)
		if !ex {
			return time.Time{}, false
		}
		if reset.IsZero() {
			reset = nextMonth(now)
		}
		return reset, true
	}
	return time.Time{}, false
}

func (b *Brave) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// pausedUntil returns the end of the current pause, or false when Brave may
// be called. An expired pause is cleared.
func (b *Brave) pausedUntil() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.until.IsZero() {
		return time.Time{}, false
	}
	if !b.now().Before(b.until) {
		b.until = time.Time{}
		log.Println("cover: brave: quota reset time passed; searching with Brave again")
		return time.Time{}, false
	}
	return b.until, true
}

// pause starts a pause after a quota error and tells the notifier, once
// per pause: a request that was already in flight when another one paused
// Brave finds the pause in place and stays quiet.
func (b *Brave) pause(until time.Time) {
	b.mu.Lock()
	if !b.until.IsZero() && b.now().Before(b.until) {
		b.mu.Unlock()
		return
	}
	b.until = until
	b.mu.Unlock()
	log.Printf("cover: brave: monthly credit used up; paused until %s, Unsplash and the other libraries are used meanwhile",
		until.UTC().Format(time.RFC3339))
	if b.Notify != nil {
		b.Notify.BraveQuotaExhausted(until)
	}
}
