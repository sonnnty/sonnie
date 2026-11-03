package usage

import (
	"reflect"
	"testing"
)

func TestFormatMarker(t *testing.T) {
	got := FormatMarker(State{Month: "2026-11", Unit: UnitChars, Value: 12345, Alerted: []int{80, 50}})
	if want := "sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 audio_ms=0 alerted=50,80"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = FormatMarker(State{Month: "2026-11", Unit: UnitTHBMicro, Value: 240000000, AudioMs: 90500, Alerted: []int{50}})
	if want := "sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=90500 alerted=50"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// An unset unit is written as chars, never as an empty unit.
	got = FormatMarker(State{Month: "2026-11"})
	if want := "sonnie-tts-usage v2 month=2026-11 unit=chars value=0 audio_ms=0 alerted="; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseMarker(t *testing.T) {
	chars := func(v int64, alerted ...int) State {
		return State{Month: "2026-11", Unit: UnitChars, Value: v, Alerted: alerted}
	}
	tests := []struct {
		name, content string
		want          State
		ok            bool
	}{
		{"v2 chars", "sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 alerted=50,80", chars(12345, 50, 80), true},
		{"v2 money", "sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=1500 alerted=50,80",
			State{Month: "2026-11", Unit: UnitTHBMicro, Value: 240000000, AudioMs: 1500, Alerted: []int{50, 80}}, true},
		{"v2 in status message", FormatStatus(State{Month: "2026-02", Unit: UnitTHBMicro, Value: 7, AudioMs: 9, Alerted: []int{50}}, 1000000),
			State{Month: "2026-02", Unit: UnitTHBMicro, Value: 7, AudioMs: 9, Alerted: []int{50}}, true},
		{"v2 empty alerted", "```\nsonnie-tts-usage v2 month=2026-11 unit=chars value=0 audio_ms=0 alerted=\n```", chars(0), true},
		{"v2 unknown key ignored", "sonnie-tts-usage v2 month=2026-11 unit=chars value=5 extra=x alerted=100", chars(5, 100), true},
		{"v2 CRLF", "hi\r\n```\r\nsonnie-tts-usage v2 month=2026-11 unit=chars value=5 alerted=\r\n```", chars(5), true},
		{"v2 unknown unit", "sonnie-tts-usage v2 month=2026-11 unit=usd value=5", State{}, false},
		{"v2 missing unit", "sonnie-tts-usage v2 month=2026-11 value=5", State{}, false},
		{"v2 missing value", "sonnie-tts-usage v2 month=2026-11 unit=chars", State{}, false},
		{"v2 legacy chars= key is not a value", "sonnie-tts-usage v2 month=2026-11 unit=chars chars=5", State{}, false},
		{"v2 negative value", "sonnie-tts-usage v2 month=2026-11 unit=chars value=-5", State{}, false},
		{"v2 bad audio_ms", "sonnie-tts-usage v2 month=2026-11 unit=chars value=5 audio_ms=x", State{}, false},
		// Legacy v1 markers are characters.
		{"v1", "sonnie-tts-usage v1 month=2026-11 chars=12345 alerted=50,80", chars(12345, 50, 80), true},
		{"v1 alerted missing", "sonnie-tts-usage v1 month=2026-11 chars=5", chars(5), true},
		{"v1 unknown key ignored", "sonnie-tts-usage v1 month=2026-11 chars=5 extra=x alerted=100", chars(5, 100), true},
		{"v1 ignores v2 keys", "sonnie-tts-usage v1 month=2026-11 chars=5 unit=thb_micro value=9", chars(5), true},
		{"v1 missing chars", "sonnie-tts-usage v1 month=2026-11", State{}, false},
		{"v1 negative chars", "sonnie-tts-usage v1 month=2026-11 chars=-5", State{}, false},
		{"v1 non-numeric chars", "sonnie-tts-usage v1 month=2026-11 chars=abc", State{}, false},
		{"v1 bad threshold", "sonnie-tts-usage v1 month=2026-11 chars=5 alerted=50,x", State{}, false},
		{"v1 bad month", "sonnie-tts-usage v1 month=2026-13 chars=5", State{}, false},
		{"v1 missing month", "sonnie-tts-usage v1 chars=5", State{}, false},
		{"no marker", "⚠️ Google TTS ใช้ไป 80%", State{}, false},
		{"unknown version", "sonnie-tts-usage v3 month=2026-11 unit=chars value=5", State{}, false},
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
	for _, in := range []State{
		{Month: "2027-01", Unit: UnitChars, Value: 999999, Alerted: []int{50, 80, 100}},
		{Month: "2027-01", Unit: UnitTHBMicro, Value: 299999999, AudioMs: 123456, Alerted: []int{50}},
	} {
		out, ok := ParseMarker(FormatMarker(in))
		if !ok || !reflect.DeepEqual(in, out) {
			t.Fatalf("round trip: got %+v ok=%v", out, ok)
		}
	}
}

func TestFormatInt(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 800000: "800,000", 1000000: "1,000,000", 12345678: "12,345,678"} {
		if got := formatInt(n); got != want {
			t.Errorf("formatInt(%d) = %q, want %q", n, got, want)
		}
	}
}
