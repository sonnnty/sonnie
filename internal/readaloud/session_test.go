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
	v := sessionView{TextChannelID: "t1", VoiceChannelID: "v1", Started: started, Manage: true, Participants: []string{"1", "2"}}
	e := sessionEmbed(v)

	if e.Title != "🔊 กำลังอ่านออกเสียง" {
		t.Errorf("title %q", e.Title)
	}
	if !strings.Contains(e.Description, "(2)") || !strings.Contains(e.Description, "<@1> <@2>") {
		t.Errorf("description %q", e.Description)
	}
	var all strings.Builder
	for _, f := range e.Fields {
		all.WriteString(f.Name + "=" + f.Value + "\n")
	}
	for _, want := range []string{"<#t1>", "<#v1>", "/tts join", "/tts leave", "ถูกลบเมื่อจบ"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("fields missing %q:\n%s", want, all.String())
		}
	}
	if e.Color != colorActive {
		t.Errorf("colour %#x", e.Color)
	}
	m, ok := ParseSessionMarkerFromEmbed(e)
	if !ok || !m.Open || !m.Manage || m.Voice != "v1" || !reflect.DeepEqual(m.Participants, v.Participants) {
		t.Errorf("marker %+v ok=%v", m, ok)
	}

	v.Manage = false
	e = sessionEmbed(v)
	if e.Color != colorWarning || !strings.Contains(e.Fields[len(e.Fields)-1].Value, "Manage Messages") {
		t.Errorf("missing-permission embed: colour %#x, %+v", e.Color, e.Fields)
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
