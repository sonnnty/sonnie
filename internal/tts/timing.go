package tts

import (
	"sync"
	"time"
)

// timingRingSize is how many recent utterance timings are kept.
const timingRingSize = 20

// Timing is the latency of one synthesized utterance (see logTiming).
type Timing struct {
	At         time.Time
	Path       string // "rest", "stream" or "rest-fallback"
	FirstAudio time.Duration
	Total      time.Duration
	Bytes      int
}

var timings struct {
	mu   sync.Mutex
	ring []Timing // oldest first, at most timingRingSize
}

func recordTiming(t Timing) {
	timings.mu.Lock()
	defer timings.mu.Unlock()
	if len(timings.ring) == timingRingSize {
		copy(timings.ring, timings.ring[1:])
		timings.ring = timings.ring[:timingRingSize-1]
	}
	timings.ring = append(timings.ring, t)
}

// RecentTimings returns the latest utterance timings of this process, newest
// first, for the dashboard.
func RecentTimings() []Timing {
	timings.mu.Lock()
	defer timings.mu.Unlock()
	out := make([]Timing, len(timings.ring))
	for i, t := range timings.ring {
		out[len(out)-1-i] = t
	}
	return out
}
