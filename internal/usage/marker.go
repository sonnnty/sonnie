package usage

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	markerPrefixV1 = "sonnie-tts-usage v1"
	markerPrefixV2 = "sonnie-tts-usage v2"
)

// Units a meter can count in. The marker carries the unit so that a value is
// never read as the wrong thing after the owner switches voices.
const (
	// UnitChars counts characters sent to Google (Chirp/Neural2 voices).
	UnitChars = "chars"
	// UnitTHBMicro counts spend in millionths of a baht (Gemini-TTS).
	UnitTHBMicro = "thb_micro"
)

// State is the persisted part of the meter: what the status message in the
// alert channel records.
type State struct {
	// Month is the billing month as "YYYY-MM" (US/Pacific).
	Month string
	// Unit is UnitChars or UnitTHBMicro and says what Value counts.
	Unit string
	// Value is the amount used this month, in Unit.
	Value int64
	// AudioMs is the audio generated this month in milliseconds (Gemini
	// only; 0 otherwise).
	AudioMs int64
	// Alerted lists the thresholds (percent) already announced this month.
	Alerted []int
}

// FormatMarker renders s as the machine-readable marker line.
func FormatMarker(s State) string {
	alerted := append([]int(nil), s.Alerted...)
	sort.Ints(alerted)
	parts := make([]string, len(alerted))
	for i, a := range alerted {
		parts[i] = strconv.Itoa(a)
	}
	unit := s.Unit
	if unit == "" {
		unit = UnitChars
	}
	return fmt.Sprintf("%s month=%s unit=%s value=%d audio_ms=%d alerted=%s",
		markerPrefixV2, s.Month, unit, s.Value, s.AudioMs, strings.Join(parts, ","))
}

// ParseMarker finds the marker line in a status message and parses it. It
// accepts the current v2 format and the legacy v1 format (characters only,
// `chars=N`, which reads as Unit=UnitChars). It reports false when the content
// has no valid marker.
func ParseMarker(content string) (State, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "`")
		line = strings.TrimSpace(line)
		for _, version := range []struct {
			prefix string
			v1     bool
		}{{markerPrefixV2, false}, {markerPrefixV1, true}} {
			if !strings.HasPrefix(line, version.prefix+" ") {
				continue
			}
			if s, ok := parseMarkerFields(strings.Fields(strings.TrimPrefix(line, version.prefix)), version.v1); ok {
				return s, true
			}
		}
	}
	return State{}, false
}

func parseMarkerFields(fields []string, v1 bool) (State, bool) {
	var (
		s                State
		haveMonth, haveN bool
	)
	if v1 {
		s.Unit = UnitChars
	}
	for _, f := range fields {
		key, value, ok := strings.Cut(f, "=")
		if !ok {
			return State{}, false
		}
		switch key {
		case "month":
			if _, err := time.Parse("2006-01", value); err != nil {
				return State{}, false
			}
			s.Month, haveMonth = value, true
		case "chars":
			if !v1 {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return State{}, false
			}
			s.Value, haveN = n, true
		case "unit":
			if v1 {
				continue
			}
			if value != UnitChars && value != UnitTHBMicro {
				return State{}, false
			}
			s.Unit = value
		case "value", "audio_ms":
			if v1 {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return State{}, false
			}
			if key == "value" {
				s.Value, haveN = n, true
			} else {
				s.AudioMs = n
			}
		case "alerted":
			if value == "" {
				continue
			}
			for _, p := range strings.Split(value, ",") {
				n, err := strconv.Atoi(p)
				if err != nil || n <= 0 || n > 100 {
					return State{}, false
				}
				s.Alerted = append(s.Alerted, n)
			}
			sort.Ints(s.Alerted)
		}
		// Unknown keys are ignored so a later writer can add fields.
	}
	return s, haveMonth && haveN && s.Unit != ""
}
