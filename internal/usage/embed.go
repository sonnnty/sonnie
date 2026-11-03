package usage

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Discord embed colors by usage percentage:
// green < 50%, yellow 50–79%, orange 80–99%, red ≥ 100%.
const (
	ColorGreen  = 0x57F287
	ColorYellow = 0xFEE75C
	ColorOrange = 0xE67E22
	ColorRed    = 0xED4245
)

// StatusFooterHuman is the human-facing text shown in the status message footer.
const StatusFooterHuman = "อัปเดตอัตโนมัติ · อย่าลบข้อความนี้"

var thaiMonthNames = [...]string{
	"",
	"มกราคม",
	"กุมภาพันธ์",
	"มีนาคม",
	"เมษายน",
	"พฤษภาคม",
	"มิถุนายน",
	"กรกฎาคม",
	"สิงหาคม",
	"กันยายน",
	"ตุลาคม",
	"พฤศจิกายน",
	"ธันวาคม",
}

// ThaiMonth returns the Thai name for month m.
func ThaiMonth(m time.Month) string {
	if m >= 1 && int(m) < len(thaiMonthNames) {
		return thaiMonthNames[m]
	}
	return m.String()
}

// FormatTitle returns the embed title for month YYYY-MM in Thai,
// e.g. "🔊 Google TTS · พฤศจิกายน 2026".
func FormatTitle(month string) string {
	if t, err := time.Parse("2006-01", month); err == nil {
		return fmt.Sprintf("🔊 Google TTS · %s %d", ThaiMonth(t.Month()), t.Year())
	}
	return fmt.Sprintf("🔊 Google TTS · %s", month)
}

// EmbedColor returns the Discord embed color corresponding to percent:
// green < 50, yellow 50–79, orange 80–99, red ≥ 100.
func EmbedColor(percent int) int {
	switch {
	case percent >= 100:
		return ColorRed
	case percent >= 80:
		return ColorOrange
	case percent >= 50:
		return ColorYellow
	default:
		return ColorGreen
	}
}

// ProgressBar returns a Unicode progress bar of length segments using
// black parallelogram (▰) and white parallelogram (▱).
// Clamped to [0, segments].
func ProgressBar(percent, segments int) string {
	if segments <= 0 {
		segments = 10
	}
	filled := percent * segments / 100
	if filled < 0 {
		filled = 0
	}
	if filled > segments {
		filled = segments
	}
	empty := segments - filled
	return strings.Repeat("▰", filled) + strings.Repeat("▱", empty)
}

// FormatProgressBar returns a 10-segment progress bar followed by two spaces
// and the percentage, e.g. "▰▰▱▱▱▱▱▱▱▱  12%".
func FormatProgressBar(percent int) string {
	return fmt.Sprintf("%s  %d%%", ProgressBar(percent, 10), percent)
}

// FormatAudioDuration formats milliseconds into Thai duration text:
// e.g. "6.7 วินาที", "12 นาที 5 วินาที", "1 ชั่วโมง 2 นาที 3 วินาที".
func FormatAudioDuration(ms int64) string {
	sec := ms / 1000
	if sec >= 3600 {
		hr := sec / 3600
		min := (sec % 3600) / 60
		remSec := sec % 60
		if remSec > 0 {
			return fmt.Sprintf("%d ชั่วโมง %d นาที %d วินาที", hr, min, remSec)
		}
		if min > 0 {
			return fmt.Sprintf("%d ชั่วโมง %d นาที", hr, min)
		}
		return fmt.Sprintf("%d ชั่วโมง", hr)
	}
	if sec >= 60 {
		min := sec / 60
		remSec := sec % 60
		if remSec > 0 {
			return fmt.Sprintf("%d นาที %d วินาที", min, remSec)
		}
		return fmt.Sprintf("%d นาที", min)
	}
	return fmt.Sprintf("%s วินาที", FormatSeconds(ms))
}

// NextResetUnix calculates the Unix timestamp for the start of the next calendar
// month in America/Los_Angeles (US/Pacific billing time zone).
func NextResetUnix(month string, now time.Time) int64 {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.FixedZone("PST", -8*60*60)
	}
	t, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		t = now.In(loc)
		t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	}
	return t.AddDate(0, 1, 0).Unix()
}

// FormatUsageEmbed builds a compact Thai embed representing a usage snapshot.
func FormatUsageEmbed(snap Snapshot, now time.Time) *discordgo.MessageEmbed {
	var desc string
	if snap.Limit > 0 {
		desc = FormatProgressBar(snap.Percent) + "\n"
		if snap.Unit == UnitTHBMicro {
			desc += fmt.Sprintf("%s / %s", FormatTHB(snap.Used), FormatTHB(snap.Limit))
		} else {
			desc += fmt.Sprintf("%s / %s ตัวอักษร", formatInt(snap.Used), formatInt(snap.Limit))
		}
	} else {
		if snap.Unit == UnitTHBMicro {
			desc = fmt.Sprintf("%s (ไม่จำกัดงบ)", FormatTHB(snap.Used))
		} else {
			desc = fmt.Sprintf("%s ตัวอักษร (ไม่จำกัดโควตา)", formatInt(snap.Used))
		}
	}

	audioVal := "-"
	if snap.Unit == UnitTHBMicro || snap.AudioMs > 0 {
		audioVal = FormatAudioDuration(snap.AudioMs)
	}

	statusVal := "🟢 กำลังอ่าน"
	if snap.Paused {
		if snap.Unit == UnitChars {
			statusVal = "⏸️ หยุดอ่าน (ครบโควตา)"
		} else {
			statusVal = "⏸️ หยุดอ่าน (ครบงบ)"
		}
	}

	resetVal := fmt.Sprintf("<t:%d:R>", NextResetUnix(snap.Month, now))

	return &discordgo.MessageEmbed{
		Title:       FormatTitle(snap.Month),
		Description: desc,
		Color:       EmbedColor(snap.Percent),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "เสียงที่สร้าง",
				Value:  audioVal,
				Inline: true,
			},
			{
				Name:   "สถานะ",
				Value:  statusVal,
				Inline: true,
			},
			{
				Name:   "รีเซ็ต",
				Value:  resetVal,
				Inline: true,
			},
		},
	}
}

// FormatStatusEmbed builds the persistent status embed.
//
// Marker placement:
// We place the machine-readable marker on the second line of the embed footer
// text, after "อัปเดตอัตโนมัติ · อย่าลบข้อความนี้". In Discord, footer text is rendered
// in a small, muted secondary font at the very bottom of the embed, which makes it
// far less visible and obtrusive than an embed field (which has prominent bold titles
// and full-size text). It avoids any visible code block while staying completely
// reliable to parse on startup.
func FormatStatusEmbed(st State, limit int64, now time.Time) *discordgo.MessageEmbed {
	percent := 0
	if limit > 0 {
		if st.Value >= limit {
			percent = 100
		} else {
			percent = int(st.Value * 100 / limit)
		}
	}
	snap := Snapshot{
		Month:   st.Month,
		Unit:    st.Unit,
		Used:    st.Value,
		Limit:   limit,
		Percent: percent,
		Paused:  limit > 0 && st.Value >= limit,
		AudioMs: st.AudioMs,
	}
	embed := FormatUsageEmbed(snap, now)
	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: fmt.Sprintf("%s\n%s", StatusFooterHuman, FormatMarker(st)),
	}
	embed.Timestamp = now.Format(time.RFC3339)
	return embed
}

// ParseMarkerFromEmbed extracts and parses the State from an embed.
// It checks the footer text first, followed by fields and description.
func ParseMarkerFromEmbed(embed *discordgo.MessageEmbed) (State, bool) {
	if embed == nil {
		return State{}, false
	}
	if embed.Footer != nil && embed.Footer.Text != "" {
		if s, ok := ParseMarker(embed.Footer.Text); ok {
			return s, true
		}
	}
	for _, f := range embed.Fields {
		if f != nil && f.Value != "" {
			if s, ok := ParseMarker(f.Value); ok {
				return s, true
			}
		}
	}
	if embed.Description != "" {
		if s, ok := ParseMarker(embed.Description); ok {
			return s, true
		}
	}
	return State{}, false
}

// AlertPreviewText returns a concise Thai single-line alert for notification previews.
func AlertPreviewText(mode Mode, threshold int) string {
	if mode == ModeMoney {
		if threshold >= 100 {
			return "🛑 Google TTS ใช้ครบงบเดือนนี้แล้ว — หยุดอ่านออกเสียงจนกว่าจะถึงเดือนหน้า"
		}
		return fmt.Sprintf("⚠️ Google TTS ใช้ไป %d%% ของงบเดือนนี้", threshold)
	}
	if threshold >= 100 {
		return "🛑 Google TTS ใช้ครบโควตาเดือนนี้แล้ว — หยุดอ่านออกเสียงจนกว่าจะถึงเดือนหน้า"
	}
	return fmt.Sprintf("⚠️ Google TTS ใช้ไป %d%% ของโควตาเดือนนี้", threshold)
}

// FormatAlertEmbed returns a compact embed for a threshold alert (50%, 80%, 100%).
func FormatAlertEmbed(mode Mode, threshold int, used, limit int64, month string) *discordgo.MessageEmbed {
	percent := threshold
	if limit > 0 && used > 0 {
		calc := int(used * 100 / limit)
		if calc > percent {
			percent = calc
		}
	}
	desc := FormatProgressBar(percent) + "\n"
	if mode == ModeMoney {
		desc += fmt.Sprintf("%s / %s", FormatTHB(used), FormatTHB(limit))
	} else {
		desc += fmt.Sprintf("%s / %s ตัวอักษร", formatInt(used), formatInt(limit))
	}
	return &discordgo.MessageEmbed{
		Title:       FormatTitle(month),
		Description: desc,
		Color:       EmbedColor(threshold),
	}
}
