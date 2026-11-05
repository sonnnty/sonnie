package usage

import (
	"strconv"
	"unicode/utf8"
)

func runeCount(s string) int { return utf8.RuneCountInString(s) }

// formatInt renders n with thousands separators, e.g. 1,000,000.
func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := false
	if n < 0 {
		neg, s = true, s[1:]
	}
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// FormatCount formats n with thousands separators, e.g. "1,234,567".
func FormatCount(n int64) string { return formatInt(n) }
