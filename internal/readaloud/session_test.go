package readaloud

import (
	"github.com/bwmarrin/discordgo"

	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var markerStart = time.Unix(1793000000, 0)

func twoChannelMarker() SessionMarker {
	return SessionMarker{
		Version:      2,
		Open:         true,
		Started:      markerStart,
		Voice:        "v1",
		Participants: []string{"1", "2"},
		Reading:      map[string]string{"1": "t1", "2": "t2"},
		Left:         []string{"3"},
		Channels:     []MarkerChannel{{ID: "t1", Manage: true}, {ID: "t2", Manage: false}},
		Binds: []MarkerBind{
			{User: "1", Channel: "t1"}, // from the card
			{User: "2", Channel: "t1", Since: markerStart.Add(90 * time.Second)},
			{User: "2", Channel: "t2", Since: markerStart.Add(10 * time.Minute)},
			{User: "3", Channel: "t2", Since: markerStart.Add(11 * time.Minute)},
		},
	}
}

func TestSessionMarker_RoundTripV2(t *testing.T) {
	in := twoChannelMarker()
	line := FormatSessionMarker(in)
	if !strings.HasPrefix(line, "sonnie-tts-session v2 ") {
		t.Fatalf("line %q is not v2", line)
	}
	got, ok := ParseSessionMarker("อัปเดตอัตโนมัติ · อย่าลบข้อความนี้\n" + line)
	if !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, in)
	}

	in.Open, in.Participants, in.Reading, in.Left, in.Channels, in.Binds = false, nil, nil, nil, nil, nil
	got, ok = ParseSessionMarker(FormatSessionMarker(in))
	if !ok || got.Open || len(got.All()) != 0 || got.Version != 2 {
		t.Fatalf("ended marker: got %+v ok=%v", got, ok)
	}
}

func TestParseSessionMarker_V1StillParses(t *testing.T) {
	line := "sonnie-tts-session v1 state=open started=1793000000 voice=v1 manage=1 participants=1,2 left=3"
	got, ok := ParseSessionMarker("footer text\n" + line)
	want := SessionMarker{Version: 1, Open: true, Started: markerStart, Voice: "v1", Manage: true,
		Participants: []string{"1", "2"}, Left: []string{"3"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
	if got.Channels != nil || got.Binds != nil || got.Reading != nil {
		t.Fatalf("a v1 marker has no channel list: %+v", got)
	}

	got, ok = ParseSessionMarker("sonnie-tts-session v1 state=ended started=1793000000 voice=v1 manage=0 participants= left=")
	if !ok || got.Open || got.Manage || len(got.All()) != 0 {
		t.Fatalf("ended v1 marker: %+v ok=%v", got, ok)
	}
}

func TestParseSessionMarker_RejectsGarbage(t *testing.T) {
	for _, s := range []string{
		"",
		"sonnie-tts-usage v2 month=2026-11 unit=chars value=1 audio_ms=0 alerted=",
		"sonnie-tts-session v1 state=open",                     // no start time
		"sonnie-tts-session v2 state=open",                     // no start time
		"sonnie-tts-session v1 state=maybe started=1793000000", // unknown state
		"sonnie-tts-session v2 state=open started=soon",
		"sonnie-tts-session v3 state=open started=1793000000", // unknown version
		"sonnie-tts-session state=open started=1793000000",
	} {
		if m, ok := ParseSessionMarker(s); ok {
			t.Errorf("%q parsed as %+v", s, m)
		}
	}
}

func TestParseSessionMarker_V2IgnoresBrokenReferences(t *testing.T) {
	got, ok := ParseSessionMarker("sonnie-tts-session v2 state=open started=1793000000 voice=v1 cards=t1:1 participants=1:0,2:7,3 left= binds=1:0:-,1:5:a,1:0:zz!,bad")
	if !ok {
		t.Fatal("not parsed")
	}
	if !reflect.DeepEqual(got.Participants, []string{"1", "2", "3"}) || !reflect.DeepEqual(got.Reading, map[string]string{"1": "t1"}) {
		t.Errorf("participants %v reading %v", got.Participants, got.Reading)
	}
	if !reflect.DeepEqual(got.Binds, []MarkerBind{{User: "1", Channel: "t1"}}) {
		t.Errorf("binds %+v", got.Binds)
	}
}

func TestFormatSessionMarker_StaysWithinFooterLimit(t *testing.T) {
	m := SessionMarker{Open: true, Started: markerStart, Voice: "v", Participants: []string{"1"},
		Reading:  map[string]string{"1": "t1"},
		Channels: []MarkerChannel{{ID: "t1", Manage: true}},
		Binds:    []MarkerBind{{User: "1", Channel: "t1"}},
	}
	for i := 0; i < 200; i++ {
		u := fmt.Sprintf("1234567890123%05d", i)
		m.Left = append(m.Left, u)
		m.Binds = append(m.Binds, MarkerBind{User: u, Channel: "t1", Since: markerStart.Add(time.Duration(i) * time.Minute)})
	}
	line := FormatSessionMarker(m)
	if len(line) > maxMarkerLen {
		t.Fatalf("marker is %d bytes", len(line))
	}
	got, ok := ParseSessionMarker(line)
	if !ok || !reflect.DeepEqual(got.Participants, []string{"1"}) {
		t.Fatalf("participants lost: %+v ok=%v", got, ok)
	}
	// The oldest left users go first, with their binds; the newest stay.
	if n := len(got.Left); n == 0 || got.Left[n-1] != m.Left[199] || len(got.Binds) != 1+n {
		t.Fatalf("left %d, binds %d", n, len(got.Binds))
	}
}

func twoChannelView() sessionView {
	return sessionView{VoiceChannelID: "v1", Started: markerStart}.
		join("1", "t1", true, time.Time{}).
		join("2", "t2", false, markerStart.Add(time.Minute)).
		join("3", "t1", true, markerStart.Add(2*time.Minute))
}

func TestSessionView_JoinMoveLeave(t *testing.T) {
	v := twoChannelView()
	if ch, ok := v.channelOf("2"); !ok || ch != "t2" || len(v.Channels) != 2 || !v.manageIn("t1") || v.manageIn("t2") {
		t.Fatalf("view %+v", v)
	}

	moved := v.join("2", "t1", true, markerStart.Add(5*time.Minute))
	if ch, _ := moved.channelOf("2"); ch != "t1" || len(moved.Members) != 3 {
		t.Fatalf("moved %+v", moved.Members)
	}
	if !moved.hasBind("2", "t1") || !moved.hasBind("2", "t2") {
		t.Fatalf("a moved user keeps both binds: %+v", moved.Binds)
	}
	if ch, _ := v.channelOf("2"); ch != "t2" {
		t.Fatal("join must not change the original view")
	}

	back := moved.join("2", "t2", false, markerStart.Add(9*time.Minute))
	if got := back.bindsIn("t2")["2"]; !got.Equal(markerStart.Add(time.Minute)) {
		t.Fatalf("a return keeps the first join time, got %v", got)
	}

	left := v.leave("2")
	if _, ok := left.channelOf("2"); ok || !reflect.DeepEqual(left.Left, []string{"2"}) || !left.hasBind("2", "t2") {
		t.Fatalf("left %+v", left)
	}
	if got := left.join("2", "t2", false, markerStart.Add(time.Hour)); len(got.Left) != 0 || len(got.Members) != 3 {
		t.Fatalf("rejoin %+v", got)
	}
	if got, want := left.everyone(), []string{"1", "3", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("everyone %v, want %v", got, want)
	}
}

func TestSessionEmbed(t *testing.T) {
	v := twoChannelView()
	v.PerMessage = true
	e := sessionEmbed(v, "t1")

	if e.Title != "" {
		t.Errorf("title %q: the card has no title, so its marker URL stays hidden", e.Title)
	}
	wantDesc := "🔈 <#v1> · <t:1793000000:R>\n👥 <@1> <@3> · <#t1>\n<@2> · <#t2>"
	if e.Description != wantDesc {
		t.Errorf("description %q, want %q", e.Description, wantDesc)
	}
	if len(e.Fields) != 0 {
		t.Errorf("the embed has no fields, got %+v", e.Fields)
	}
	human := e.Footer.Text
	if want := "/tts join เพื่อให้อ่าน · /tts leave เพื่อออก · ลบข้อความหลังอ่าน"; human != want {
		t.Errorf("footer %q, want %q", human, want)
	}
	if e.Color != colorActive {
		t.Errorf("colour %#x", e.Color)
	}
	m, ok := ParseSessionMarkerFromEmbed(e)
	if !ok || !m.Open || m.Voice != "v1" || len(m.Channels) != 2 || !reflect.DeepEqual(m.Participants, []string{"1", "2", "3"}) || m.Reading["2"] != "t2" {
		t.Errorf("marker %+v ok=%v", m, ok)
	}
	if e.Description != sessionEmbed(v, "t2").Description {
		t.Error("every card lists the same participants")
	}

	// Without per-message deletion the footer says when messages go.
	v.PerMessage = false
	if human, _, _ := strings.Cut(sessionEmbed(v, "t1").Footer.Text, "\n"); !strings.HasSuffix(human, "ลบข้อความเมื่อจบ") {
		t.Errorf("footer %q", human)
	}

	// The warning is per card: t2 has no Manage Messages.
	e = sessionEmbed(v, "t2")
	human, _, _ = strings.Cut(e.Footer.Text, "\n")
	if e.Color != colorWarning || !strings.HasSuffix(human, "⚠️ ไม่มีสิทธิ์ลบข้อความ") || !strings.HasPrefix(human, "/tts join") {
		t.Errorf("missing-permission embed: colour %#x, footer %q", e.Color, human)
	}
	if _, ok := ParseSessionMarkerFromEmbed(e); !ok {
		t.Error("the marker must survive the warning footer")
	}
}

func TestSessionEmbed_CapsLongLists(t *testing.T) {
	v := sessionView{VoiceChannelID: "v1", Started: markerStart}
	for i := 0; i < maxListedMentions+5; i++ {
		v = v.join(fmt.Sprintf("u%d", i), "t1", true, time.Time{})
	}
	e := sessionEmbed(v, "t1")
	if !strings.Contains(e.Description, "และอีก 5 คน") || strings.Contains(e.Description, fmt.Sprintf("<@u%d>", maxListedMentions)) {
		t.Errorf("description %q", e.Description)
	}
}

func TestSessionEmbed_NoParticipants(t *testing.T) {
	e := sessionEmbed(sessionView{VoiceChannelID: "v1", Started: markerStart}, "t1")
	if !strings.HasSuffix(e.Description, "\n👥 -") {
		t.Errorf("description %q", e.Description)
	}
}

func TestSummaryEmbed_MarksSessionEnded(t *testing.T) {
	v := sessionView{VoiceChannelID: "v1", Started: markerStart}.join("1", "t1", true, time.Time{})
	e := summaryEmbed(v, summary{Participants: []string{"1"}, Duration: 5 * time.Minute}, time.Unix(1793000300, 0))
	m, ok := ParseSessionMarkerFromEmbed(e)
	if !ok || m.Open || len(m.All()) != 0 {
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

func TestSessionEmbed_MarkerIsHiddenInTheURL(t *testing.T) {
	v := twoChannelView()
	for _, e := range []*discordgo.MessageEmbed{
		sessionEmbed(v, "t1"),
		summaryEmbed(v, summary{}, markerStart.Add(time.Hour)),
	} {
		if strings.Contains(e.Footer.Text, sessionMarkerPrefix) || strings.Contains(e.Description, sessionMarkerPrefix) {
			t.Errorf("marker shown: footer %q", e.Footer.Text)
		}
		if !strings.HasPrefix(e.URL, markerURLPrefix) || e.Title != "" {
			t.Errorf("url %q title %q", e.URL, e.Title)
		}
		if _, ok := ParseSessionMarkerFromEmbed(e); !ok {
			t.Error("marker not readable back")
		}
	}
	// The longest marker still fits Discord's embed URL (2048 is safe; 2027 was checked live).
	big := v
	for i := 0; i < 200; i++ {
		big = big.join(fmt.Sprintf("1%017d", i), "t1", true, markerStart.Add(time.Duration(i)*time.Minute)).leave(fmt.Sprintf("1%017d", i))
	}
	if u := sessionEmbed(big, "t1").URL; len(u) > 2048 {
		t.Errorf("url is %d characters", len(u))
	}
}

func TestParseSessionMarkerFromEmbed_OldFooterCards(t *testing.T) {
	v := twoChannelView()
	old := &discordgo.MessageEmbed{Footer: &discordgo.MessageEmbedFooter{
		Text: "/tts join เพื่อให้อ่าน\n" + FormatSessionMarker(v.marker(true)),
	}}
	if m, ok := ParseSessionMarkerFromEmbed(old); !ok || !m.Open || m.Voice != "v1" {
		t.Fatalf("marker %+v ok=%v", m, ok)
	}
}
