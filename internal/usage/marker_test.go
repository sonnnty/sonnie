package usage

import (
	"reflect"
	"testing"
)

func TestFormatMarker(t *testing.T) {
	got := FormatMarker(State{Month: "2026-11", Chars: 12345, Alerted: []int{80, 50}})
	if want := "sonnie-tts-usage v1 month=2026-11 chars=12345 alerted=50,80"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = FormatMarker(State{Month: "2026-11"})
	if want := "sonnie-tts-usage v1 month=2026-11 chars=0 alerted="; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseMarker(t *testing.T) {
	tests := []struct {
		name, content string
		want          State
		ok            bool
	}{
		{"bare", "sonnie-tts-usage v1 month=2026-11 chars=12345 alerted=50,80", State{"2026-11", 12345, []int{50, 80}}, true},
		{"in status message", FormatStatus(State{"2026-02", 7, []int{50}}, 1000000), State{"2026-02", 7, []int{50}}, true},
		{"empty alerted", "```\nsonnie-tts-usage v1 month=2026-11 chars=0 alerted=\n```", State{Month: "2026-11"}, true},
		{"alerted missing", "sonnie-tts-usage v1 month=2026-11 chars=5", State{Month: "2026-11", Chars: 5}, true},
		{"unknown key ignored", "sonnie-tts-usage v1 month=2026-11 chars=5 extra=x alerted=100", State{"2026-11", 5, []int{100}}, true},
		{"CRLF", "hi\r\n```\r\nsonnie-tts-usage v1 month=2026-11 chars=5 alerted=\r\n```", State{Month: "2026-11", Chars: 5}, true},
		{"no marker", "⚠️ Google TTS ใช้ไป 80%", State{}, false},
		{"wrong version", "sonnie-tts-usage v2 month=2026-11 chars=5", State{}, false},
		{"bad month", "sonnie-tts-usage v1 month=2026-13 chars=5", State{}, false},
		{"negative chars", "sonnie-tts-usage v1 month=2026-11 chars=-5", State{}, false},
		{"non-numeric chars", "sonnie-tts-usage v1 month=2026-11 chars=abc", State{}, false},
		{"bad threshold", "sonnie-tts-usage v1 month=2026-11 chars=5 alerted=50,x", State{}, false},
		{"missing chars", "sonnie-tts-usage v1 month=2026-11", State{}, false},
		{"missing month", "sonnie-tts-usage v1 chars=5", State{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseMarker(tt.content)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	in := State{Month: "2027-01", Chars: 999999, Alerted: []int{50, 80, 100}}
	out, ok := ParseMarker(FormatMarker(in))
	if !ok || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: got %+v ok=%v", out, ok)
	}
}

func TestFormatInt(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 800000: "800,000", 1000000: "1,000,000", 12345678: "12,345,678"} {
		if got := formatInt(n); got != want {
			t.Errorf("formatInt(%d) = %q, want %q", n, got, want)
		}
	}
}
