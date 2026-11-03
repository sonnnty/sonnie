package usage

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const markerPrefix = "sonnie-tts-usage v1"

// State is the persisted part of the meter: what the status message in the
// alert channel records.
type State struct {
	// Month is the billing month as "YYYY-MM" (US/Pacific).
	Month string
	// Chars is the number of characters sent to Google this month.
	Chars int64
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
	return fmt.Sprintf("%s month=%s chars=%d alerted=%s", markerPrefix, s.Month, s.Chars, strings.Join(parts, ","))
}

// ParseMarker finds the marker line in a status message and parses it. It
// reports false when the content has no valid v1 marker.
func ParseMarker(content string) (State, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "`")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, markerPrefix+" ") {
			continue
		}
		if s, ok := parseMarkerFields(strings.Fields(strings.TrimPrefix(line, markerPrefix))); ok {
			return s, true
		}
	}
	return State{}, false
}

func parseMarkerFields(fields []string) (State, bool) {
	var (
		s                State
		haveMonth, haveN bool
	)
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
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return State{}, false
			}
			s.Chars, haveN = n, true
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
	return s, haveMonth && haveN
}
