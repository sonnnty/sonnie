package usage

import (
	"github.com/bwmarrin/discordgo"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		percent int
		want    string
	}{
		{0, "▱▱▱▱▱▱▱▱▱▱"},
		{1, "▱▱▱▱▱▱▱▱▱▱"},
		{49, "▰▰▰▰▱▱▱▱▱▱"},
		{50, "▰▰▰▰▰▱▱▱▱▱"},
		{99, "▰▰▰▰▰▰▰▰▰▱"},
		{100, "▰▰▰▰▰▰▰▰▰▰"},
		{150, "▰▰▰▰▰▰▰▰▰▰"},
		{-10, "▱▱▱▱▱▱▱▱▱▱"},
	}
	for _, tt := range tests {
		t.Run(string(rune(tt.percent)), func(t *testing.T) {
			got := ProgressBar(tt.percent, 10)
			if got != tt.want {
				t.Errorf("ProgressBar(%d, 10) = %q, want %q", tt.percent, got, tt.want)
			}
		})
	}
}

func TestFormatProgressBar(t *testing.T) {
	tests := []struct {
		percent int
		want    string
	}{
		{0, "▱▱▱▱▱▱▱▱▱▱  0%"},
		{1, "▱▱▱▱▱▱▱▱▱▱  1%"},
		{12, "▰▱▱▱▱▱▱▱▱▱  12%"},
		{49, "▰▰▰▰▱▱▱▱▱▱  49%"},
		{50, "▰▰▰▰▰▱▱▱▱▱  50%"},
		{99, "▰▰▰▰▰▰▰▰▰▱  99%"},
		{100, "▰▰▰▰▰▰▰▰▰▰  100%"},
		{150, "▰▰▰▰▰▰▰▰▰▰  150%"},
	}
	for _, tt := range tests {
		got := FormatProgressBar(tt.percent)
		if got != tt.want {
			t.Errorf("FormatProgressBar(%d) = %q, want %q", tt.percent, got, tt.want)
		}
	}
}

func TestEmbedColor(t *testing.T) {
	tests := []struct {
		percent int
		want    int
	}{
		{0, ColorGreen},
		{1, ColorGreen},
		{49, ColorGreen},
		{50, ColorYellow},
		{79, ColorYellow},
		{80, ColorOrange},
		{99, ColorOrange},
		{100, ColorRed},
		{150, ColorRed},
	}
	for _, tt := range tests {
		if got := EmbedColor(tt.percent); got != tt.want {
			t.Errorf("EmbedColor(%d) = %#x, want %#x", tt.percent, got, tt.want)
		}
	}
}

func TestThaiMonths(t *testing.T) {
	wantMonths := map[time.Month]string{
		time.January:   "มกราคม",
		time.February:  "กุมภาพันธ์",
		time.March:     "มีนาคม",
		time.April:     "เมษายน",
		time.May:       "พฤษภาคม",
		time.June:      "มิถุนายน",
		time.July:      "กรกฎาคม",
		time.August:    "สิงหาคม",
		time.September: "กันยายน",
		time.October:   "ตุลาคม",
		time.November:  "พฤศจิกายน",
		time.December:  "ธันวาคม",
	}
	for m, want := range wantMonths {
		if got := ThaiMonth(m); got != want {
			t.Errorf("ThaiMonth(%v) = %q, want %q", m, got, want)
		}
	}

	if got := FormatTitle("2026-11"); got != "🔊 Google TTS · พฤศจิกายน 2026" {
		t.Fatalf("FormatTitle(2026-11) = %q, want %q", got, "🔊 Google TTS · พฤศจิกายน 2026")
	}
	if got := FormatTitle("2027-01"); got != "🔊 Google TTS · มกราคม 2027" {
		t.Fatalf("FormatTitle(2027-01) = %q, want %q", got, "🔊 Google TTS · มกราคม 2027")
	}
}

func TestFormatAudioDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0.0 วินาที"},
		{6700, "6.7 วินาที"},
		{12300, "12.3 วินาที"},
		{59900, "59.9 วินาที"},
		{60000, "1 นาที"},
		{65000, "1 นาที 5 วินาที"},
		{725000, "12 นาที 5 วินาที"},
		{3600000, "1 ชั่วโมง"},
		{3660000, "1 ชั่วโมง 1 นาที"},
		{3665000, "1 ชั่วโมง 1 นาที 5 วินาที"},
	}
	for _, tt := range tests {
		if got := FormatAudioDuration(tt.ms); got != tt.want {
			t.Errorf("FormatAudioDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestNextResetUnix(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	ts := NextResetUnix("2026-11", now)

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2026, 12, 1, 0, 0, 0, 0, loc).Unix()
	if ts != expected {
		t.Fatalf("NextResetUnix = %d, want %d", ts, expected)
	}
}

func TestMarkerRoundTripThroughEmbed(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	states := []State{
		{Month: "2026-11", Unit: UnitTHBMicro, Value: 36200000, AudioMs: 6700, Alerted: []int{50}},
		{Month: "2026-11", Unit: UnitChars, Value: 123456, Alerted: []int{50, 80}},
		{Month: "2027-01", Unit: UnitTHBMicro, Value: 300000000, AudioMs: 120000, Alerted: []int{50, 80, 100}},
	}
	for _, st := range states {
		embed := FormatStatusEmbed(st, 300000000, now)
		if embed.Footer == nil || !strings.Contains(embed.Footer.Text, StatusFooterHuman) {
			t.Fatalf("footer missing human text: %+v", embed.Footer)
		}
		if embed.Timestamp != now.Format(time.RFC3339) {
			t.Fatalf("timestamp mismatch: got %q, want %q", embed.Timestamp, now.Format(time.RFC3339))
		}

		parsed, ok := ParseMarkerFromEmbed(embed)
		if !ok {
			t.Fatalf("failed to parse marker from embed for %+v", st)
		}
		if !reflect.DeepEqual(parsed, st) {
			t.Fatalf("parsed %+v != original %+v", parsed, st)
		}
	}
}

func TestFormatUsageEmbed_StructureAndFields(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	// Money mode, active
	snapMoney := Snapshot{
		Month:   "2026-11",
		Unit:    UnitTHBMicro,
		Used:    36200000,
		Limit:   300000000,
		Percent: 12,
		Paused:  false,
		AudioMs: 6700,
	}
	embed := FormatUsageEmbed(snapMoney, now)
	if embed.Title != "🔊 Google TTS · พฤศจิกายน 2026" {
		t.Errorf("title = %q", embed.Title)
	}
	if embed.Color != ColorGreen {
		t.Errorf("color = %#x, want %#x", embed.Color, ColorGreen)
	}
	if !strings.Contains(embed.Description, "▰▱▱▱▱▱▱▱▱▱  12%") || !strings.Contains(embed.Description, "฿36.20 / ฿300.00") {
		t.Errorf("description: %q", embed.Description)
	}
	if len(embed.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(embed.Fields))
	}
	if embed.Fields[0].Name != "เสียงที่สร้าง" || embed.Fields[0].Value != "6.7 วินาที" || !embed.Fields[0].Inline {
		t.Errorf("field 0: %+v", embed.Fields[0])
	}
	if embed.Fields[1].Name != "สถานะ" || embed.Fields[1].Value != "🟢 กำลังอ่าน" || !embed.Fields[1].Inline {
		t.Errorf("field 1: %+v", embed.Fields[1])
	}
	if embed.Fields[2].Name != "รีเซ็ต" || !strings.HasPrefix(embed.Fields[2].Value, "<t:") || !embed.Fields[2].Inline {
		t.Errorf("field 2: %+v", embed.Fields[2])
	}

	// Chars mode, paused
	snapChars := Snapshot{
		Month:   "2026-11",
		Unit:    UnitChars,
		Used:    1000000,
		Limit:   1000000,
		Percent: 100,
		Paused:  true,
		AudioMs: 0,
	}
	embedChars := FormatUsageEmbed(snapChars, now)
	if embedChars.Color != ColorRed {
		t.Errorf("color = %#x, want %#x", embedChars.Color, ColorRed)
	}
	if !strings.Contains(embedChars.Description, "1,000,000 / 1,000,000 ตัวอักษร") {
		t.Errorf("description: %q", embedChars.Description)
	}
	if embedChars.Fields[0].Value != "-" {
		t.Errorf("char mode audio field = %q, want -", embedChars.Fields[0].Value)
	}
	if embedChars.Fields[1].Value != "⏸️ หยุดอ่าน (ครบโควตา)" {
		t.Errorf("char mode paused status = %q", embedChars.Fields[1].Value)
	}
}

func TestAlertMessages(t *testing.T) {
	// Money mode
	p50 := AlertPreviewText(ModeMoney, 50)
	if p50 != "⚠️ Google TTS ใช้ไป 50% ของงบเดือนนี้" {
		t.Errorf("preview 50 = %q", p50)
	}
	p100 := AlertPreviewText(ModeMoney, 100)
	if !strings.Contains(p100, "🛑 Google TTS ใช้ครบงบเดือนนี้แล้ว") {
		t.Errorf("preview 100 = %q", p100)
	}

	// Char mode
	pc50 := AlertPreviewText(ModeChars, 50)
	if pc50 != "⚠️ Google TTS ใช้ไป 50% ของโควตาเดือนนี้" {
		t.Errorf("char preview 50 = %q", pc50)
	}
	pc100 := AlertPreviewText(ModeChars, 100)
	if !strings.Contains(pc100, "🛑 Google TTS ใช้ครบโควตาเดือนนี้แล้ว") {
		t.Errorf("char preview 100 = %q", pc100)
	}

	// Alert embeds
	a50 := FormatAlertEmbed(ModeMoney, 50, 150000000, 300000000, "2026-11")
	if a50.Color != ColorYellow {
		t.Errorf("a50 color = %#x, want %#x", a50.Color, ColorYellow)
	}
	if !strings.Contains(a50.Description, "฿150.00 / ฿300.00") {
		t.Errorf("a50 desc = %q", a50.Description)
	}

	a100 := FormatAlertEmbed(ModeMoney, 100, 300000000, 300000000, "2026-11")
	if a100.Color != ColorRed {
		t.Errorf("a100 color = %#x, want %#x", a100.Color, ColorRed)
	}
}

func TestFormatStatusEmbed_MarkerHiddenInURL(t *testing.T) {
	st := State{Month: "2026-11", Unit: UnitTHBMicro, Value: 4464032, AudioMs: 519876}
	e := FormatStatusEmbed(st, 300_000_000, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	if e.Title != "" || !strings.HasPrefix(e.Description, "### "+FormatTitle("2026-11")+"\n") {
		t.Fatalf("title %q description %q", e.Title, e.Description)
	}
	if e.Footer.Text != StatusFooterHuman || strings.Contains(e.Description, "sonnie-tts-usage") {
		t.Fatalf("marker visible: footer %q", e.Footer.Text)
	}
	got, ok := ParseMarkerFromEmbed(e)
	if !ok || got.Value != st.Value || got.AudioMs != st.AudioMs || got.Month != st.Month {
		t.Fatalf("marker %+v ok=%v", got, ok)
	}
	// Status messages from before keep the marker in the footer and are still read.
	old := &discordgo.MessageEmbed{Footer: &discordgo.MessageEmbedFooter{Text: StatusFooterHuman + "\n" + FormatMarker(st)}}
	if got, ok := ParseMarkerFromEmbed(old); !ok || got.Value != st.Value {
		t.Fatalf("old marker %+v ok=%v", got, ok)
	}
}
