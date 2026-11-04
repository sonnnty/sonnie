package events

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/eventtime"
	"github.com/Sonty15/SONNIE/internal/modal"
)

// formJSON is the modal of the last response, as Discord receives it.
func formJSON(t *testing.T, r *discordgo.InteractionResponse) map[string]any {
	t.Helper()
	if r.Type != discordgo.InteractionResponseModal {
		t.Fatalf("response type %d, want a modal", r.Type)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m["data"].(map[string]any)
}

// formInputs are the inputs of the modal by custom ID, with the label of
// the Label around each.
func formInputs(t *testing.T, data map[string]any) (map[string]map[string]any, []string) {
	t.Helper()
	inputs := map[string]map[string]any{}
	var labels []string
	for _, c := range data["components"].([]any) {
		label := c.(map[string]any)
		if label["type"] != float64(modal.LabelComponent) {
			t.Fatalf("top-level component %v is not a Label", label)
		}
		in := label["component"].(map[string]any)
		inputs[in["custom_id"].(string)] = in
		labels = append(labels, label["label"].(string))
	}
	return inputs, labels
}

func TestCreate_NoOptionsOpensForm(t *testing.T) {
	hs := newHarness()
	var asked []string
	hs.h.SetVoiceChannel(func(guild, user string) string {
		asked = append(asked, guild+"/"+user)
		return "777"
	})
	hs.h.HandleCommand(command("42", permCreate, "create"))

	if len(hs.api.responses) != 1 || len(hs.api.created) != 0 || len(hs.covers.queries) != 0 {
		t.Fatalf("%d responses, %d created", len(hs.api.responses), len(hs.api.created))
	}
	if len(asked) != 1 || asked[0] != guildID+"/42" {
		t.Fatalf("voice channel lookups %v", asked)
	}
	data := formJSON(t, hs.lastResponse(t))
	if data["custom_id"] != "event:form:1" || data["title"] != "สร้าง Event" {
		t.Fatalf("modal %v %v", data["custom_id"], data["title"])
	}
	inputs, labels := formInputs(t, data)
	if got := strings.Join(labels, ","); got != "ชื่อ event,วัน,เวลา,ห้องเสียง,รายละเอียด" {
		t.Fatalf("labels %s", got)
	}

	name := inputs["name"]
	if name["type"] != 4.0 || name["style"] != 1.0 || name["required"] != true || name["max_length"] != 100.0 {
		t.Errorf("name %v", name)
	}
	when := inputs["time"]
	if when["type"] != 4.0 || when["style"] != 1.0 || when["required"] != true || when["value"] != "20:00" || when["placeholder"] != "20:00 / 3 ทุ่ม / บ่าย 2" {
		t.Errorf("time %v", when)
	}
	desc := inputs["description"]
	if desc["type"] != 4.0 || desc["style"] != 2.0 || desc["required"] != false || desc["max_length"] != 1000.0 {
		t.Errorf("description %v", desc)
	}

	day := inputs["day"]
	opts := day["options"].([]any)
	if day["type"] != 3.0 || day["required"] != true || len(opts) != 25 {
		t.Fatalf("day %v with %d options", day, len(opts))
	}
	first := opts[0].(map[string]any)
	if first["label"] != "วันนี้ (อังคาร 3 พ.ย.)" || first["value"] != "2026-11-03" || first["default"] != true {
		t.Errorf("first day %v", first)
	}
	for _, o := range opts[1:] {
		if _, ok := o.(map[string]any)["default"]; ok {
			t.Errorf("more than one default day: %v", o)
		}
	}

	place := inputs["place"]
	if place["type"] != 8.0 || place["required"] != false || place["min_values"] != 0.0 || place["max_values"] != 1.0 {
		t.Errorf("place %v", place)
	}
	if types := place["channel_types"].([]any); len(types) != 1 || types[0] != float64(discordgo.ChannelTypeGuildVoice) {
		t.Errorf("channel types %v", types)
	}
	defaults, _ := place["default_values"].([]any)
	if len(defaults) != 1 || defaults[0].(map[string]any)["id"] != "777" || defaults[0].(map[string]any)["type"] != "channel" {
		t.Errorf("default channel %v", place["default_values"])
	}
}

func TestCreate_FormWithoutVoiceChannel(t *testing.T) {
	for label, lookup := range map[string]VoiceChannelFunc{
		"no lookup":    nil,
		"not in voice": func(string, string) string { return "" },
		"not an ID":    func(string, string) string { return "<#1>" },
	} {
		hs := newHarness()
		hs.h.SetVoiceChannel(lookup)
		hs.h.HandleCommand(command("42", permCreate, "create"))
		inputs, _ := formInputs(t, formJSON(t, hs.lastResponse(t)))
		if v, ok := inputs["place"]["default_values"]; ok {
			t.Errorf("%s: default channel %v", label, v)
		}
	}
}

func TestCreate_FormRefusedWithoutPermission(t *testing.T) {
	hs := newHarness()
	hs.h.HandleCommand(command("42", discordgo.PermissionSendMessages, "create"))
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgNoCreate {
		t.Fatalf("reply %q", got)
	}
}

func TestCreate_AnyOptionSkipsForm(t *testing.T) {
	hs := newHarness()
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "เตะบอล")))
	if got := ephemeralText(t, hs.lastResponse(t)); !strings.Contains(got, eventtime.ErrEmpty.Error()) {
		t.Fatalf("name only: %q", got)
	}
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y")))
	for _, r := range hs.api.responses {
		if r.Type == discordgo.InteractionResponseModal {
			t.Fatal("opened the form although options were given")
		}
	}
	if len(hs.api.created) != 1 || hs.api.created[0].EntityMetadata.Location != "y" {
		t.Fatal("options path did not create the event")
	}
}

func TestCommandDefinition_CreateOptionsAreOptional(t *testing.T) {
	for _, sub := range CommandDefinition().Options {
		if sub.Name != "create" {
			continue
		}
		for _, o := range sub.Options {
			if o.Required {
				t.Errorf("create option %s is required; /event create alone must open the form", o.Name)
			}
		}
	}
}

func TestDayOptions_MonthEnds(t *testing.T) {
	tests := []struct {
		now    time.Time
		labels map[int]string // option index -> label
		values map[int]string
	}{
		{
			now:    time.Date(2026, 11, 28, 10, 0, 0, 0, eventtime.Bangkok),
			labels: map[int]string{0: "วันนี้ (เสาร์ 28 พ.ย.)", 1: "พรุ่งนี้ (อาทิตย์ 29 พ.ย.)", 2: "จันทร์ 30 พ.ย.", 3: "อังคาร 1 ธ.ค.", 24: "อังคาร 22 ธ.ค."},
			values: map[int]string{3: "2026-12-01", 24: "2026-12-22"},
		},
		{
			now:    time.Date(2026, 12, 31, 23, 59, 0, 0, eventtime.Bangkok),
			labels: map[int]string{0: "วันนี้ (พฤหัส 31 ธ.ค.)", 1: "พรุ่งนี้ (ศุกร์ 1 ม.ค.)", 2: "เสาร์ 2 ม.ค."},
			values: map[int]string{0: "2026-12-31", 1: "2027-01-01"},
		},
		{
			// 00:30 in Bangkok is still the previous day in UTC.
			now:    time.Date(2026, 11, 30, 17, 30, 0, 0, time.UTC),
			labels: map[int]string{0: "วันนี้ (อังคาร 1 ธ.ค.)", 1: "พรุ่งนี้ (พุธ 2 ธ.ค.)"},
			values: map[int]string{0: "2026-12-01"},
		},
		{
			now:    time.Date(2028, 2, 28, 12, 0, 0, 0, eventtime.Bangkok),
			labels: map[int]string{1: "พรุ่งนี้ (อังคาร 29 ก.พ.)", 2: "พุธ 1 มี.ค."},
			values: map[int]string{1: "2028-02-29"},
		},
	}
	for _, tt := range tests {
		opts := dayOptions(tt.now)
		if len(opts) != 25 || !opts[0].Default {
			t.Fatalf("%s: %d options, first default %v", tt.now, len(opts), opts[0].Default)
		}
		for i, want := range tt.labels {
			if opts[i].Label != want {
				t.Errorf("%s: option %d label %q, want %q", tt.now, i, opts[i].Label, want)
			}
		}
		for i, want := range tt.values {
			if opts[i].Value != want {
				t.Errorf("%s: option %d value %q, want %q", tt.now, i, opts[i].Value, want)
			}
		}
	}
}

func TestCheckFormID(t *testing.T) {
	if err := checkFormID("event:form:1"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]error{
		"event:form:0":                           errOldForm,
		"event:form:2":                           errOldForm,
		"event:form:":                            errOldForm,
		"event:cancel:1":                         errBadForm,
		"other:form:1":                           errBadForm,
		"":                                       errBadForm,
		"event:form:" + strings.Repeat("9", 100): errBadForm,
	} {
		if err := checkFormID(id); !errors.Is(err, want) {
			t.Errorf("checkFormID(%q) = %v, want %v", id, err, want)
		}
	}
}

// formSubmit is a submitted create form.
func formSubmit(userID string, perms int64, customID string, values map[string]string) (*discordgo.InteractionCreate, modal.Submit) {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: guildID, Member: member(userID, perms),
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID},
	}}
	s := modal.Submit{CustomID: customID, Values: map[string][]string{}}
	for k, v := range values {
		if k == fieldPlace || k == fieldDay {
			if v == "" {
				s.Values[k] = []string{}
				continue
			}
		}
		s.Values[k] = []string{v}
	}
	return i, s
}

func TestFormSubmit_VoiceEvent(t *testing.T) {
	hs := newHarness()
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, formID, map[string]string{
		"name": " เตะบอล ", "day": "2026-11-06", "time": "3 ทุ่ม", "place": "777", "description": "มาเตะกัน",
	}))
	if r := hs.api.responses[0]; r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("first response type %d flags %d, want a public deferral", r.Type, r.Data.Flags)
	}
	if len(hs.api.created) != 1 {
		t.Fatal("no event created")
	}
	p := hs.api.created[0]
	start := time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok)
	if p.Name != "เตะบอล" || p.EntityType != discordgo.GuildScheduledEventEntityTypeVoice || p.ChannelID != "777" || p.EntityMetadata != nil {
		t.Fatalf("params %+v", p)
	}
	if !p.ScheduledStartTime.Equal(start) || !p.ScheduledEndTime.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("times %s – %s", p.ScheduledStartTime, p.ScheduledEndTime)
	}
	if !strings.HasPrefix(p.Description, "มาเตะกัน\n\n📷") || p.Image == "" {
		t.Fatalf("description %q, image %v", p.Description, p.Image != "")
	}
	if got := strings.Join(hs.covers.queries, ","); got != "kw(เตะบอล)" {
		t.Fatalf("cover queries %s", got)
	}
	evs := hs.sched.Upcoming(guildID)
	if len(evs) != 1 || hs.sched.Creator(evs[0]) != "42" {
		t.Fatal("event not tracked with its creator")
	}
	e := hs.lastEdit(t)
	if *e.Content != "" {
		t.Fatalf("content %q", *e.Content)
	}
	if ids := buttonIDs(*e.Components); len(ids) != 1 || ids[0] != recoverID(evs[0].ID) {
		t.Fatalf("buttons %v (want 🔄 เปลี่ยนรูป)", ids)
	}
}

func TestFormSubmit_NoChannelIsExternalDiscord(t *testing.T) {
	hs := newHarness()
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, formID, map[string]string{
		"name": "คุยเล่น", "day": "2026-11-03", "time": "20:00", "place": "", "description": "",
	}))
	if len(hs.api.created) != 1 {
		t.Fatal("no event created")
	}
	p := hs.api.created[0]
	if p.EntityType != discordgo.GuildScheduledEventEntityTypeExternal || p.ChannelID != "" || p.EntityMetadata == nil || p.EntityMetadata.Location != "Discord" {
		t.Fatalf("params %+v", p)
	}
	start := time.Date(2026, 11, 3, 20, 0, 0, 0, eventtime.Bangkok)
	if !p.ScheduledStartTime.Equal(start) || p.ScheduledEndTime == nil || !p.ScheduledEndTime.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("times %s – %v", p.ScheduledStartTime, p.ScheduledEndTime)
	}
	if got := *hs.lastEdit(t).Content; !strings.Contains(got, "`Discord`") || !strings.Contains(got, "/event edit location:") {
		t.Fatalf("reply does not say it is external: %q", got)
	}

	// The note goes with a cover warning on separate lines.
	hs.covers.autoErr = errors.New("down")
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, formID, map[string]string{"name": "x", "day": "2026-11-04", "time": "20:00"}))
	if got := *hs.lastEdit(t).Content; !strings.Contains(got, "`Discord`") || !strings.Contains(got, "\n⚠️ หารูปปกไม่เจอ") {
		t.Fatalf("reply %q", got)
	}
}

func TestFormSubmit_Rejected(t *testing.T) {
	tests := []struct {
		label  string
		values map[string]string
		want   []string // in the reply
		field  string
	}{
		{"past time today", map[string]string{"name": "x", "day": "2026-11-03", "time": "10:00"},
			[]string{"“10:00”", "วันนี้ (อังคาร 3 พ.ย.)", "ผ่านไปแล้ว"}, "time"},
		{"exactly now", map[string]string{"name": "x", "day": "2026-11-03", "time": "หกโมงเย็น"},
			[]string{"“หกโมงเย็น”", "ผ่านไปแล้ว"}, "time"},
		{"yesterday (form opened before midnight)", map[string]string{"name": "x", "day": "2026-11-02", "time": "20:00"},
			[]string{"“20:00”", "จันทร์ 2 พ.ย.", "ผ่านไปแล้ว"}, "time"},
		{"date in time", map[string]string{"name": "x", "day": "2026-11-04", "time": "พรุ่งนี้ 20:00"},
			[]string{"“พรุ่งนี้ 20:00”", "ช่องเวลาใส่แค่เวลา"}, "time"},
		{"unreadable", map[string]string{"name": "x", "day": "2026-11-04", "time": "หลังเลิกงาน"},
			[]string{"“หลังเลิกงาน”", "`3 ทุ่ม`"}, "time"},
		{"empty time", map[string]string{"name": "x", "day": "2026-11-04", "time": " "},
			[]string{eventtime.ErrEmpty.Error()}, "time"},
		{"no name", map[string]string{"name": "  ", "day": "2026-11-04", "time": "20:00"},
			[]string{"ใส่ชื่อ"}, "name"},
		{"no day", map[string]string{"name": "x", "day": "", "time": "20:00"},
			[]string{"วันที่เลือกใช้ไม่ได้"}, "day"},
		{"bad day", map[string]string{"name": "x", "day": "2026-13-01", "time": "20:00"},
			[]string{"วันที่เลือกใช้ไม่ได้"}, "day"},
		{"day beyond the list", map[string]string{"name": "x", "day": "2026-11-28", "time": "20:00"},
			[]string{"วันที่เลือกใช้ไม่ได้"}, "day"},
		{"bad channel", map[string]string{"name": "x", "day": "2026-11-04", "time": "20:00", "place": "abc"},
			[]string{errBadForm.Error()}, "place"},
	}
	for _, tt := range tests {
		logs := captureLog(t)
		hs := newHarness()
		hs.h.HandleModalSubmit(formSubmit("42", permCreate, formID, tt.values))
		if len(hs.api.responses) != 1 || len(hs.api.created) != 0 {
			t.Errorf("%s: %d responses, %d created", tt.label, len(hs.api.responses), len(hs.api.created))
			continue
		}
		reply := ephemeralText(t, hs.lastResponse(t))
		for _, w := range tt.want {
			if !strings.Contains(reply, w) {
				t.Errorf("%s: reply %q lacks %q", tt.label, reply, w)
			}
		}
		l := logs.lines("rejected /event create form input from user 42: field " + tt.field + ",")
		if len(l) != 1 {
			t.Errorf("%s: log %v", tt.label, logs.lines("rejected"))
		}
	}
	// The last day of the list is fine.
	hs := newHarness()
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, formID, map[string]string{"name": "x", "day": "2026-11-27", "time": "20:00"}))
	if len(hs.api.created) != 1 {
		t.Fatal("last day of the list refused")
	}
}

func TestFormSubmit_BadFormAndPermissions(t *testing.T) {
	values := map[string]string{"name": "x", "day": "2026-11-04", "time": "20:00"}
	hs := newHarness()
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, "event:form:0", values))
	if got := ephemeralText(t, hs.lastResponse(t)); got != errOldForm.Error() {
		t.Fatalf("old form: %q", got)
	}
	hs.h.HandleModalSubmit(formSubmit("42", permCreate, "event:cancel:5", values))
	if got := ephemeralText(t, hs.lastResponse(t)); got != errBadForm.Error() {
		t.Fatalf("bad form: %q", got)
	}
	hs.h.HandleModalSubmit(formSubmit("42", discordgo.PermissionSendMessages, formID, values))
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgNoCreate {
		t.Fatalf("no permission: %q", got)
	}
	i, s := formSubmit("42", permCreate, formID, values)
	i.GuildID, i.Member = "", nil
	hs.h.HandleModalSubmit(i, s)
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgGuildOnly {
		t.Fatalf("DM: %q", got)
	}
	if len(hs.api.created) != 0 {
		t.Fatal("created an event")
	}
}
