package events

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
)

func TestParseCustomID(t *testing.T) {
	valid := map[string]customID{
		"event:pick:0a1b2c3d:123456789012345678:1": {kind: kindPick, picker: "0a1b2c3d", eventID: "123456789012345678", choice: 1},
		"event:pick:0a1b2c3d:1:5":                  {kind: kindPick, picker: "0a1b2c3d", eventID: "1", choice: 5},
		"event:more:0a1b2c3d:42":                   {kind: kindMore, picker: "0a1b2c3d", eventID: "42"},
		"event:recover:42":                         {kind: kindRecover, eventID: "42"},
		"event:cancel:42":                          {kind: kindCancel, eventID: "42"},
		"event:keep:42":                            {kind: kindKeep, eventID: "42"},
	}
	for s, want := range valid {
		got, err := parseCustomID(s)
		if err != nil || got != want {
			t.Errorf("parseCustomID(%q) = %+v, %v", s, got, err)
		}
	}
	for _, s := range []string{
		"", "event:", "tts:pick:0a1b2c3d:1:1", "event:pick:0a1b2c3d:1:0", "event:pick:0a1b2c3d:1:6",
		"event:pick:0a1b2c3d:1:01", "event:pick:0a1b2c3d:1:x", "event:pick:0A1B2C3D:1:1", "event:pick:abc:1:1",
		"event:pick:0a1b2c3d:e1:1", "event:pick:0a1b2c3d:1", "event:pick:0a1b2c3d:1:1:9", "event:more:0a1b2c3d",
		"event:cancel:", "event:cancel:abc", "event:delete:42", "event:recover:42:1",
		"event:cancel:" + strings.Repeat("1", 99),
	} {
		if _, err := parseCustomID(s); err == nil {
			t.Errorf("parseCustomID(%q) accepted", s)
		}
	}
	// What the handler sends parses back.
	for _, s := range []string{pickID("0a1b2c3d", "99", 3), moreID("0a1b2c3d", "99"), recoverID("99"), cancelID("99"), keepID("99")} {
		if _, err := parseCustomID(s); err != nil {
			t.Errorf("own ID %q rejected", s)
		}
	}
}

// openCoverPicker runs /event cover and returns the picker's button IDs.
func openCoverPicker(t *testing.T, hs *harness, userID string, perms int64, eventID string) []string {
	t.Helper()
	hs.h.HandleCommand(command(userID, perms, "cover", strOpt("event", eventID)))
	if r := hs.api.responses[len(hs.api.responses)-1]; r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("response %d flags %d, want an ephemeral deferral", r.Type, r.Data.Flags)
	}
	e := hs.lastEdit(t)
	if len(*e.Embeds) != pickerSize {
		t.Fatalf("%d candidate embeds, want %d", len(*e.Embeds), pickerSize)
	}
	for n, em := range *e.Embeds {
		if em.Image == nil || em.Image.URL == "" || em.Title != string(rune('1'+n)) {
			t.Fatalf("candidate embed %d: %+v", n, em)
		}
	}
	return buttonIDs(*e.Components)
}

func TestCoverPicker_PickApplies(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	ids := openCoverPicker(t, hs, "77", 0, "5001")
	if len(ids) != pickerSize+1 {
		t.Fatalf("buttons %v", ids)
	}
	if got := strings.Join(hs.covers.searches, ","); got != "kw(เตะบอล)#1" {
		t.Fatalf("searches %s", got)
	}
	pick2, err := parseCustomID(ids[1])
	if err != nil || pick2.kind != kindPick || pick2.choice != 2 || pick2.eventID != "5001" {
		t.Fatalf("button 2 %q: %+v %v", ids[1], pick2, err)
	}

	hs.h.HandleComponent(click("77", 0, ids[1]))
	if r := hs.lastResponse(t); r.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("response %d, want a deferred update", r.Type)
	}
	if got := strings.Join(hs.covers.fetched, ","); got != "p1-2" {
		t.Fatalf("fetched %s", got)
	}
	ev := hs.api.get("5001")
	if ev.Image == "" || ev.Description != "เดิม\n\n📷 “Photo p1-2” — Ann · CC BY 2.0 · https://src/p1-2" {
		t.Fatalf("event image %q description %q", ev.Image, ev.Description)
	}
	e := hs.lastEdit(t)
	if !strings.Contains(*e.Content, "เปลี่ยนรูปปกแล้ว") || len(*e.Components) != 0 {
		t.Fatalf("confirmation %q, %d components", *e.Content, len(*e.Components))
	}

	// The picker is used up.
	hs.h.HandleComponent(click("77", 0, ids[0]))
	if r := hs.lastResponse(t); r.Type != discordgo.InteractionResponseUpdateMessage || !strings.Contains(r.Data.Content, "หมดเวลา") {
		t.Fatalf("second pick: %d %q", r.Type, r.Data.Content)
	}
	if len(hs.covers.fetched) != 1 {
		t.Fatal("fetched again")
	}
}

func TestCoverPicker_MoreShowsNextPageThenWraps(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	ids := openCoverPicker(t, hs, "u", permManage, "5001")
	more := ids[len(ids)-1]
	hs.h.HandleComponent(click("u", permManage, more))
	if r := hs.lastResponse(t); r.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("response %d", r.Type)
	}
	e := hs.lastEdit(t)
	if !strings.Contains(*e.Content, "หน้า 2") {
		t.Fatalf("content %q", *e.Content)
	}
	ids2 := buttonIDs(*e.Components)
	// Page 3 is empty: back to page 1 with a note.
	hs.h.HandleComponent(click("u", permManage, ids2[len(ids2)-1]))
	e = hs.lastEdit(t)
	if !strings.Contains(*e.Content, "หน้า 1") || !strings.Contains(*e.Content, "ไม่มีรูปเพิ่มแล้ว") {
		t.Fatalf("content %q", *e.Content)
	}
	if got := strings.Join(hs.covers.searches, ","); got != "kw(เตะบอล)#1,kw(เตะบอล)#2,kw(เตะบอล)#3,kw(เตะบอล)#1" {
		t.Fatalf("searches %s", got)
	}
	// The old page's buttons no longer work.
	hs.h.HandleComponent(click("u", permManage, ids[0]))
	if r := hs.lastResponse(t); !strings.Contains(r.Data.Content, "หมดเวลา") {
		t.Fatalf("old button: %q", r.Data.Content)
	}
}

func TestCoverPicker_KeywordsOption(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("77", 0, "cover", strOpt("event", "5001"), strOpt("keywords", "soccer night")))
	if got := strings.Join(hs.covers.searches, ","); got != "soccer night#1" {
		t.Fatalf("searches %s", got)
	}
}

func TestCoverPicker_FailedDownloadKeepsPicker(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	ids := openCoverPicker(t, hs, "77", 0, "5001")
	hs.h.HandleComponent(click("77", 0, ids[2])) // the fake fails candidate 3
	e := hs.lastEdit(t)
	if !strings.Contains(*e.Content, "โหลดรูปที่ 3 ไม่ได้") || len(buttonIDs(*e.Components)) != pickerSize+1 {
		t.Fatalf("content %q", *e.Content)
	}
	hs.h.HandleComponent(click("77", 0, ids[0]))
	if hs.api.get("5001").Image == "" {
		t.Fatal("could not pick another after a failure")
	}
}

func TestCoverPicker_ValidatesButtons(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	seed(hs, "5002", "77")
	ids := openCoverPicker(t, hs, "77", 0, "5001")
	c, _ := parseCustomID(ids[0])

	tests := map[string]struct {
		user   string
		perms  int64
		id     string
		expect string
	}{
		"other user":      {"u", permManage, ids[0], "เป็นของคนที่เปิด"},
		"other event":     {"77", 0, pickID(c.picker, "5002", 1), msgBadButton},
		"unknown picker":  {"77", 0, pickID("ffffffff", "5001", 1), "หมดเวลา"},
		"malformed":       {"77", 0, "event:pick:" + c.picker + ":5001:9", msgBadButton},
		"not an event id": {"77", 0, "event:pick:" + c.picker + ":abc:1", msgBadButton},
	}
	for label, tt := range tests {
		hs.h.HandleComponent(click(tt.user, tt.perms, tt.id))
		r := hs.lastResponse(t)
		if !strings.Contains(r.Data.Content, tt.expect) {
			t.Errorf("%s: reply %q, want %q", label, r.Data.Content, tt.expect)
		}
	}
	if len(hs.covers.fetched) != 0 {
		t.Fatal("a rejected button fetched a picture")
	}
}

func TestCoverPicker_ChoiceBeyondCandidates(t *testing.T) {
	hs := newHarness()
	ev := seed(hs, "5001", "77")
	p := picker{guildID: guildID, eventID: ev.ID, userID: "77", query: "q", page: 1, cands: []cover.Candidate{{ID: "only"}}}
	token := hs.h.pickers.put(p)
	hs.h.HandleComponent(click("77", 0, pickID(token, ev.ID, 4)))
	if r := hs.lastResponse(t); r.Data.Content != msgBadButton {
		t.Fatalf("reply %q", r.Data.Content)
	}
}

func TestCoverPicker_Expires(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	ids := openCoverPicker(t, hs, "77", 0, "5001")
	hs.clock.Set(now.Add(pickerTTL + time.Second))
	hs.h.HandleComponent(click("77", 0, ids[0]))
	if r := hs.lastResponse(t); !strings.Contains(r.Data.Content, "หมดเวลา") {
		t.Fatalf("reply %q", r.Data.Content)
	}
}

func TestCoverPicker_RefusedWithoutPermission(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("u", permCreate, "cover", strOpt("event", "5001")))
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgNoManage {
		t.Fatalf("reply %q", got)
	}
	if len(hs.covers.searches) != 0 {
		t.Fatal("searched anyway")
	}
}

func TestCoverAttachment_ReplacesCredit(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77") // "เดิม" + credit
	res := &discordgo.ApplicationCommandInteractionDataResolved{Attachments: map[string]*discordgo.MessageAttachment{
		"a1": {ID: "a1", URL: "https://cdn/up.png", ContentType: "image/png", Size: 100},
	}}
	img := &discordgo.ApplicationCommandInteractionDataOption{Name: "image", Type: discordgo.ApplicationCommandOptionAttachment, Value: "a1"}
	hs.h.HandleCommand(commandWith("77", 0, res, "cover", strOpt("event", "5001"), img))
	ev := hs.api.get("5001")
	if ev.Description != "เดิม" || ev.Image == "" {
		t.Fatalf("description %q image %q", ev.Description, ev.Image)
	}
	if len(hs.api.rawPatches) != 0 {
		t.Fatal("used a raw request when the description stays non-empty")
	}

	// A description that was only the credit is cleared with a raw PATCH.
	only := externalEvent("5003")
	only.CreatorID = "77"
	only.Description = "📷 Old · CC0 · https://old"
	hs.sched.Upsert(hs.api.add(only))
	hs.h.HandleCommand(commandWith("77", 0, res, "cover", strOpt("event", "5003"), img))
	if len(hs.api.rawPatches) != 1 {
		t.Fatalf("raw patches %v", hs.api.rawPatches)
	}
	if v, ok := hs.api.rawPatches[0]["description"]; !ok || v != nil {
		t.Fatalf("raw patch %v", hs.api.rawPatches[0])
	}
	if _, ok := hs.api.rawPatches[0]["image"]; !ok {
		t.Fatal("raw patch lost the image")
	}
	if ev := hs.api.get("5003"); ev.Description != "" || ev.Image == "" {
		t.Fatalf("description %q image %q", ev.Description, ev.Image)
	}
}

func TestChangeCoverButton(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleComponent(click("u", permCreate, recoverID("5001")))
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgNoManage {
		t.Fatalf("stranger: %q", got)
	}
	hs.h.HandleComponent(click("77", 0, recoverID("5001")))
	if r := hs.lastResponse(t); r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("response %d flags %d, want a new ephemeral message", r.Type, r.Data.Flags)
	}
	if len(*hs.lastEdit(t).Embeds) != pickerSize {
		t.Fatal("no picker")
	}
}

func TestCoverPicker_NoResults(t *testing.T) {
	hs := newHarness()
	hs.covers.pages = 0
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("77", 0, "cover", strOpt("event", "5001")))
	if e := hs.lastEdit(t); !strings.Contains(*e.Content, "ไม่พบรูป") || len(*e.Components) != 0 {
		t.Fatalf("reply %q", *e.Content)
	}
}

func TestAutocomplete(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77") // เตะบอล, Fri 6 Nov 21:00
	other := externalEvent("5002")
	other.Name = "Board games"
	other.ScheduledStartTime = now.Add(time.Hour)
	hs.sched.Upsert(hs.api.add(other))

	ac := func(typed string) []*discordgo.ApplicationCommandOptionChoice {
		i := command("u", 0, "edit", &discordgo.ApplicationCommandInteractionDataOption{
			Name: "event", Type: discordgo.ApplicationCommandOptionString, Value: typed, Focused: true})
		i.Type = discordgo.InteractionApplicationCommandAutocomplete
		hs.h.HandleAutocomplete(i)
		r := hs.lastResponse(t)
		if r.Type != discordgo.InteractionApplicationCommandAutocompleteResult {
			t.Fatalf("response %d", r.Type)
		}
		return r.Data.Choices
	}
	got := ac("")
	if len(got) != 2 || got[0].Value != "5002" || got[1].Value != "5001" {
		t.Fatalf("choices %+v", got)
	}
	if got[1].Name != "ศ. 6 พ.ย. 21:00 · เตะบอล" {
		t.Fatalf("label %q", got[1].Name)
	}
	if got := ac("BOARD"); len(got) != 1 || got[0].Value != "5002" {
		t.Fatalf("filtered %+v", got)
	}
	if got := ac("zzz"); got == nil || len(got) != 0 {
		t.Fatalf("no match %+v", got)
	}
}

func TestAutocomplete_LoadsGuildWhenEmptyAndCaps(t *testing.T) {
	hs := newHarness()
	for n := 0; n < 30; n++ {
		ev := externalEvent(string(rune('a'+n%26)) + strings.Repeat("x", n/26+1))
		ev.Name = strings.Repeat("ก", 120)
		hs.api.add(ev)
	}
	i := command("u", 0, "cancel", &discordgo.ApplicationCommandInteractionDataOption{
		Name: "event", Type: discordgo.ApplicationCommandOptionString, Value: "", Focused: true})
	i.Type = discordgo.InteractionApplicationCommandAutocomplete
	hs.h.HandleAutocomplete(i)
	choices := hs.lastResponse(t).Data.Choices
	if len(choices) != maxAutocomplete {
		t.Fatalf("%d choices", len(choices))
	}
	for _, c := range choices {
		if n := len([]rune(c.Name)); n > 100 {
			t.Fatalf("label of %d characters", n)
		}
	}
}
