package tts

import (
	"testing"
	"time"
)

func TestRecentTimings_NewestFirstAndBounded(t *testing.T) {
	timings.mu.Lock()
	timings.ring = nil
	timings.mu.Unlock()

	for i := 1; i <= timingRingSize+5; i++ {
		logTiming("stream", time.Duration(i)*time.Millisecond, time.Second, i)
	}
	got := RecentTimings()
	if len(got) != timingRingSize {
		t.Fatalf("kept %d timings", len(got))
	}
	if got[0].Bytes != timingRingSize+5 || got[len(got)-1].Bytes != 6 {
		t.Fatalf("order: first %d last %d", got[0].Bytes, got[len(got)-1].Bytes)
	}
	if got[0].Path != "stream" || got[0].FirstAudio != time.Duration(timingRingSize+5)*time.Millisecond {
		t.Fatalf("unexpected timing %+v", got[0])
	}
}
