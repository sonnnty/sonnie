package readaloud

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSessionMarker_RoundTrip(t *testing.T) {
	in := SessionMarker{
		Open:         true,
		Started:      time.Unix(1793000000, 0),
		Voice:        "v1",
		Manage:       true,
		Participants: []string{"1", "2"},
		Left:         []string{"3"},
	}
	got, ok := ParseSessionMarker("อัปเดตอัตโนมัติ · อย่าลบข้อความนี้\n" + FormatSessionMarker(in))
	if !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, in)
	}

	in.Open, in.Manage, in.Participants, in.Left = false, false, nil, nil
	got, ok = ParseSessionMarker(FormatSessionMarker(in))
	if !ok || got.Open || got.Manage || len(got.All()) != 0 {
		t.Fatalf("ended marker: got %+v ok=%v", got, ok)
	}
}

func TestParseSessionMarker_RejectsGarbage(t *testing.T) {
	for _, s := range []string{
		"",
		"sonnie-tts-usage v2 month=2026-11 unit=chars value=1 audio_ms=0 alerted=",
		"sonnie-tts-session v1 state=open",                     // no start time
		"sonnie-tts-session v1 state=maybe started=1793000000", // unknown state
		"sonnie-tts-session v1 state=open started=soon",
	} {
		if m, ok := ParseSessionMarker(s); ok {
			t.Errorf("%q parsed as %+v", s, m)
		}
	}
}

func TestFormatSessionMarker_StaysWithinFooterLimit(t *testing.T) {
	var left []string
	for i := 0; i < 200; i++ {
		left = append(left, "123456789012345678")
	}
	m := SessionMarker{Open: true, Started: time.Unix(1793000000, 0), Voice: "v", Participants: []string{"1"}, Left: left}
	line := FormatSessionMarker(m)
	if len(line) > maxMarkerLen {
		t.Fatalf("marker is %d bytes", len(line))
	}
	got, ok := ParseSessionMarker(line)
	if !ok || !reflect.DeepEqual(got.Participants, []string{"1"}) {
		t.Fatalf("participants lost: %+v ok=%v", got, ok)
	}
}

func TestSessionEmbed(t *testing.T) {
	started := time.Unix(1793000000, 0)
	v := sessionView{TextChannelID: "t1", VoiceChannelID: "v1", Started: started, Manage: true, PerMessage: true, Participants: []string{"1", "2"}}
	e := sessionEmbed(v)

	if e.Title != "🔊 กำลังอ่านออกเสียง" {
		t.Errorf("title %q", e.Title)
	}
	wantDesc := "👥 <@1> <@2>\n🔈 <#v1> · <t:1793000000:R>"
	if e.Description != wantDesc {
		t.Errorf("description %q, want %q", e.Description, wantDesc)
	}
	if len(e.Fields) != 0 {
		t.Errorf("the embed has no fields, got %+v", e.Fields)
	}
	human, _, _ := strings.Cut(e.Footer.Text, "\n")
	if want := "/tts join เพื่อให้อ่าน · /tts leave เพื่อออก · ลบข้อความหลังอ่าน"; human != want {
		t.Errorf("footer %q, want %q", human, want)
	}
	if e.Color != colorActive {
		t.Errorf("colour %#x", e.Color)
	}
	m, ok := ParseSessionMarkerFromEmbed(e)
	if !ok || !m.Open || !m.Manage || m.Voice != "v1" || !reflect.DeepEqual(m.Participants, v.Participants) {
		t.Errorf("marker %+v ok=%v", m, ok)
	}

	// Without per-message deletion the footer says when messages go.
	v.PerMessage = false
	if human, _, _ := strings.Cut(sessionEmbed(v).Footer.Text, "\n"); !strings.HasSuffix(human, "ลบข้อความเมื่อจบ") {
		t.Errorf("footer %q", human)
	}

	v.Manage = false
	e = sessionEmbed(v)
	human, _, _ = strings.Cut(e.Footer.Text, "\n")
	if e.Color != colorWarning || !strings.HasSuffix(human, "⚠️ ไม่มีสิทธิ์ลบข้อความ") || !strings.HasPrefix(human, "/tts join") {
		t.Errorf("missing-permission embed: colour %#x, footer %q", e.Color, human)
	}
	if _, ok := ParseSessionMarkerFromEmbed(e); !ok {
		t.Error("the marker must survive the warning footer")
	}
}

func TestSessionEmbed_NoParticipants(t *testing.T) {
	e := sessionEmbed(sessionView{VoiceChannelID: "v1", Started: time.Unix(1793000000, 0), Manage: true})
	if !strings.HasPrefix(e.Description, "👥 -\n") {
		t.Errorf("description %q", e.Description)
	}
}

func TestSummaryEmbed_MarksSessionEnded(t *testing.T) {
	v := sessionView{TextChannelID: "t1", VoiceChannelID: "v1", Started: time.Unix(1793000000, 0), Participants: []string{"1"}}
	e := summaryEmbed(v, summary{Participants: []string{"1"}, Duration: 5 * time.Minute}, time.Unix(1793000300, 0))
	m, ok := ParseSessionMarkerFromEmbed(e)
	if !ok || m.Open {
		t.Fatalf("summary marker %+v ok=%v; must parse as ended", m, ok)
	}
	if !strings.Contains(e.Description, "5 นาที") || len(e.Fields) != 0 {
		t.Errorf("summary %+v", e)
	}
}

func TestFormatSessionDuration(t *testing.T) {
	tests := map[time.Duration]string{
		0:                           "ไม่ถึง 1 นาที",
		29 * time.Second:            "ไม่ถึง 1 นาที",
		30 * time.Second:            "1 นาที",
		25 * time.Minute:            "25 นาที",
		time.Hour:                   "1 ชั่วโมง",
		2*time.Hour + 5*time.Minute: "2 ชั่วโมง 5 นาที",
	}
	for d, want := range tests {
		if got := formatSessionDuration(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}
