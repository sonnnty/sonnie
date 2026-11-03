package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/eventtime"
)

// now is Tuesday 3 November 2026, 18:00 in Bangkok.
var now = time.Date(2026, 11, 3, 18, 0, 0, 0, eventtime.Bangkok)

const (
	permManage = discordgo.PermissionManageEvents
	permCreate = discordgo.PermissionCreateEvents
)

type fakeCovers struct {
	queries  []string
	searches []string // "query#page"
	fetched  []string
	autoErr  error
	pages    int // how many pages have results
	fromURL  []string
}

func (f *fakeCovers) Query(_ context.Context, name, keywords string) string {
	q := keywords
	if q == "" {
		q = "kw(" + name + ")"
	}
	f.queries = append(f.queries, q)
	return q
}

func (f *fakeCovers) Search(_ context.Context, query string, page, n int) ([]cover.Candidate, error) {
	f.searches = append(f.searches, fmt.Sprintf("%s#%d", query, page))
	if page > f.pages {
		return nil, nil
	}
	var out []cover.Candidate
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("p%d-%d", page, i)
		out = append(out, cover.Candidate{Provider: "openverse", ID: id, Title: "Photo " + id, Creator: "Ann",
			License: "CC BY 2.0", SourceURL: "https://src/" + id, ImageURL: "https://img/" + id, ThumbURL: "https://thumb/" + id})
	}
	return out, nil
}

func (f *fakeCovers) Fetch(_ context.Context, c cover.Candidate) (cover.Cover, error) {
	f.fetched = append(f.fetched, c.ID)
	if strings.HasSuffix(c.ID, "-3") {
		return cover.Cover{}, errors.New("HTTP 404")
	}
	return cover.Cover{JPEG: []byte("jpeg-" + c.ID), Credit: c.Credit()}, nil
}

func (f *fakeCovers) Auto(_ context.Context, query string) (cover.Cover, error) {
	if f.autoErr != nil {
		return cover.Cover{}, f.autoErr
	}
	c := cover.Candidate{Title: "Auto", Creator: "Bob", License: "CC0", SourceURL: "https://src/auto"}
	return cover.Cover{JPEG: []byte("jpeg-auto"), Credit: c.Credit()}, nil
}

func (f *fakeCovers) FromURL(_ context.Context, url string) (cover.Cover, error) {
	f.fromURL = append(f.fromURL, url)
	return cover.Cover{JPEG: []byte("jpeg-upload")}, nil
}

type harness struct {
	api    *fakeAPI
	covers *fakeCovers
	sched  *Scheduler
	clock  *fakeClock
	h      *Handler
}

func newHarness() *harness {
	api := newFakeAPI()
	clock := &fakeClock{t: now}
	sched := newTestScheduler(api, clock, nil)
	covers := &fakeCovers{pages: 2}
	return &harness{api: api, covers: covers, sched: sched, clock: clock, h: NewHandler(api, sched, covers, clock.Now)}
}

func member(userID string, perms int64) *discordgo.Member {
	return &discordgo.Member{User: &discordgo.User{ID: userID}, Permissions: perms}
}

func strOpt(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func command(userID string, perms int64, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return commandWith(userID, perms, nil, sub, opts...)
}

func commandWith(userID string, perms int64, resolved *discordgo.ApplicationCommandInteractionDataResolved, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: guildID, Member: member(userID, perms),
		Data: discordgo.ApplicationCommandInteractionData{Name: CommandName, Resolved: resolved, Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts},
		}},
	}}
}

func click(userID string, perms int64, customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: guildID, Member: member(userID, perms),
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func (hs *harness) lastResponse(t *testing.T) *discordgo.InteractionResponse {
	t.Helper()
	if len(hs.api.responses) == 0 {
		t.Fatal("no interaction response")
	}
	return hs.api.responses[len(hs.api.responses)-1]
}

func (hs *harness) lastEdit(t *testing.T) *discordgo.WebhookEdit {
	t.Helper()
	if len(hs.api.webEdits) == 0 {
		t.Fatal("no reply edit")
	}
	return hs.api.webEdits[len(hs.api.webEdits)-1]
}

func ephemeralText(t *testing.T, r *discordgo.InteractionResponse) string {
	t.Helper()
	if r.Type != discordgo.InteractionResponseChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("response type %d flags %d, want an ephemeral message", r.Type, r.Data.Flags)
	}
	return r.Data.Content
}

func buttonIDs(comps []discordgo.MessageComponent) []string {
	var ids []string
	for _, c := range comps {
		row, ok := c.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, b := range row.Components {
			if btn, ok := b.(discordgo.Button); ok && btn.CustomID != "" {
				ids = append(ids, btn.CustomID)
			}
		}
	}
	return ids
}

// --- definition -----------------------------------------------------------

func TestCommandDefinition(t *testing.T) {
	def := CommandDefinition()
	var subs []string
	for _, sub := range def.Options {
		subs = append(subs, sub.Name)
		optional := false
		for _, o := range sub.Options {
			if n := len([]rune(o.Description)); n == 0 || n > 100 {
				t.Errorf("%s %s: description is %d characters", sub.Name, o.Name, n)
			}
			if o.Required && optional {
				t.Errorf("%s: required option %s after an optional one", sub.Name, o.Name)
			}
			optional = optional || !o.Required
			if o.Name == "event" && (!o.Autocomplete || !o.Required) {
				t.Errorf("%s: event option must be required with autocomplete", sub.Name)
			}
		}
		if n := len([]rune(sub.Description)); n == 0 || n > 100 {
			t.Errorf("%s: description is %d characters", sub.Name, n)
		}
	}
	if got := strings.Join(subs, ","); got != "create,cover,edit,cancel,list" {
		t.Fatalf("subcommands %s", got)
	}
}

// --- permissions ----------------------------------------------------------

func TestPermissions(t *testing.T) {
	tests := []struct {
		perms          int64
		user, creator  string
		create, manage bool
	}{
		{0, "u", "", false, false},
		{0, "u", "u", false, true}, // the creator
		{0, "u", "other", false, false},
		{permCreate, "u", "other", true, false},
		{permManage, "u", "other", true, true},
		{discordgo.PermissionAdministrator, "u", "", true, true},
		{discordgo.PermissionSendMessages, "", "", false, false}, // empty creator never matches
	}
	for _, tt := range tests {
		if got := canCreate(tt.perms); got != tt.create {
			t.Errorf("canCreate(%b) = %v", tt.perms, got)
		}
		if got := canManage(tt.perms, tt.user, tt.creator); got != tt.manage {
			t.Errorf("canManage(%b, %q, %q) = %v", tt.perms, tt.user, tt.creator, got)
		}
	}
}

// --- option handling ------------------------------------------------------

func createOpts(opts ...*discordgo.ApplicationCommandInteractionDataOption) options {
	_, o := subcommand(command("u", 0, "create", opts...).ApplicationCommandData())
	return o
}

func TestParseCreate(t *testing.T) {
	start := time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok)

	req, err := parseCreate(createOpts(strOpt("name", "เตะบอล"), strOpt("when", "ศุกร์นี้ 3 ทุ่ม"), strOpt("place", "123")), now)
	if err != nil {
		t.Fatal(err)
	}
	p := req.params
	if p.EntityType != discordgo.GuildScheduledEventEntityTypeVoice || p.ChannelID != "123" || p.EntityMetadata != nil {
		t.Fatalf("voice params %+v", p)
	}
	if !p.ScheduledStartTime.Equal(start) || !p.ScheduledEndTime.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("times %s – %s", p.ScheduledStartTime, p.ScheduledEndTime)
	}
	if p.PrivacyLevel != discordgo.GuildScheduledEventPrivacyLevelGuildOnly {
		t.Fatal("privacy level")
	}

	req, err = parseCreate(createOpts(strOpt("name", "ประชุม"), strOpt("when", "ศุกร์นี้ 3 ทุ่ม"), strOpt("location", "ร้านกาแฟ"),
		strOpt("end", "90 นาที"), strOpt("description", "  คุยงาน  "), strOpt("keywords", "coffee")), now)
	if err != nil {
		t.Fatal(err)
	}
	p = req.params
	if p.EntityType != discordgo.GuildScheduledEventEntityTypeExternal || p.ChannelID != "" || p.EntityMetadata.Location != "ร้านกาแฟ" {
		t.Fatalf("external params %+v", p)
	}
	if !p.ScheduledEndTime.Equal(start.Add(90 * time.Minute)) {
		t.Fatalf("end %s", p.ScheduledEndTime)
	}
	if req.body != "คุยงาน" || req.keywords != "coffee" {
		t.Fatalf("body %q keywords %q", req.body, req.keywords)
	}
}

func TestParseCreate_Errors(t *testing.T) {
	name, when := strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00")
	tests := map[string][]*discordgo.ApplicationCommandInteractionDataOption{
		"no place":        {name, when},
		"both places":     {name, when, strOpt("place", "1"), strOpt("location", "y")},
		"unreadable time": {name, strOpt("when", "เร็วๆนี้"), strOpt("location", "y")},
		"past time":       {name, strOpt("when", "วันนี้ 10:00"), strOpt("location", "y")},
		"end before":      {name, when, strOpt("location", "y"), strOpt("end", "2026-11-04 19:00")},
		"no name":         {strOpt("name", "  "), when, strOpt("location", "y")},
	}
	for label, opts := range tests {
		if _, err := parseCreate(createOpts(opts...), now); err == nil {
			t.Errorf("%s: want an error", label)
		}
	}
}

func TestParseCreate_Attachment(t *testing.T) {
	base := []*discordgo.ApplicationCommandInteractionDataOption{strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y"),
		{Name: "image", Type: discordgo.ApplicationCommandOptionAttachment, Value: "att1"}}
	parse := func(a *discordgo.MessageAttachment) (createRequest, error) {
		res := &discordgo.ApplicationCommandInteractionDataResolved{Attachments: map[string]*discordgo.MessageAttachment{}}
		if a != nil {
			res.Attachments["att1"] = a
		}
		_, o := subcommand(commandWith("u", 0, res, "create", base...).ApplicationCommandData())
		return parseCreate(o, now)
	}
	req, err := parse(&discordgo.MessageAttachment{ID: "att1", URL: "https://cdn/a.png", ContentType: "image/png", Size: 1000})
	if err != nil || req.attachment == nil || req.attachment.URL != "https://cdn/a.png" {
		t.Fatalf("png: %+v %v", req.attachment, err)
	}
	for label, a := range map[string]*discordgo.MessageAttachment{
		"gif":     {ID: "att1", ContentType: "image/gif", Size: 10},
		"pdf":     {ID: "att1", ContentType: "application/pdf", Size: 10},
		"too big": {ID: "att1", ContentType: "image/jpeg", Size: 11 << 20},
		"missing": nil,
	} {
		if _, err := parse(a); err == nil {
			t.Errorf("%s: want an error", label)
		}
	}
}

func TestSplitJoinCredit(t *testing.T) {
	credit := "📷 “Ball” — Ann · CC BY 2.0 · https://src"
	desc := joinCredit("เตะบอลกัน", credit)
	if desc != "เตะบอลกัน\n\n"+credit {
		t.Fatalf("joined %q", desc)
	}
	body, c := splitCredit(desc)
	if body != "เตะบอลกัน" || c != credit {
		t.Fatalf("split %q %q", body, c)
	}
	if body, c := splitCredit(credit); body != "" || c != credit {
		t.Fatalf("credit only: %q %q", body, c)
	}
	if body, c := splitCredit("ถ่ายรูป 📷 กัน\nบรรทัดสอง"); c != "" || body == "" {
		t.Fatalf("camera emoji in text is not a credit: %q %q", body, c)
	}
	long := joinCredit(strings.Repeat("ก", 2000), credit)
	if n := len([]rune(long)); n > maxDescriptionLen || !strings.HasSuffix(long, credit) {
		t.Fatalf("long description: %d runes, credit kept: %v", n, strings.HasSuffix(long, credit))
	}
	if joinCredit("", "") != "" || joinCredit("a", "") != "a" {
		t.Fatal("empty parts")
	}
}

// --- create ---------------------------------------------------------------

func TestCreate_RefusedWithoutPermission(t *testing.T) {
	hs := newHarness()
	hs.h.HandleCommand(command("u", discordgo.PermissionSendMessages, "create",
		strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y")))
	if got := ephemeralText(t, hs.lastResponse(t)); !strings.Contains(got, "Manage Events") {
		t.Fatalf("refusal %q", got)
	}
	if len(hs.api.created) != 0 {
		t.Fatal("created an event")
	}
}

func TestCreate_InvalidOptionsAnsweredPrivately(t *testing.T) {
	hs := newHarness()
	hs.h.HandleCommand(command("u", permCreate, "create", strOpt("name", "x"), strOpt("when", "วันนี้ 10:00"), strOpt("location", "y")))
	if got := ephemeralText(t, hs.lastResponse(t)); !strings.Contains(got, eventtime.ErrPast.Error()) {
		t.Fatalf("reply %q", got)
	}
	if len(hs.api.created) != 0 || len(hs.covers.queries) != 0 {
		t.Fatal("went on after an invalid time")
	}
}

func TestCreate_FindsCoverAndReplies(t *testing.T) {
	hs := newHarness()
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "เตะบอล"), strOpt("when", "ศุกร์นี้ 3 ทุ่ม"),
		strOpt("place", "777"), strOpt("description", "มาเตะกัน")))

	if r := hs.api.responses[0]; r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("first response type %d flags %d, want a public deferral", r.Type, r.Data.Flags)
	}
	if got := strings.Join(hs.covers.queries, ","); got != "kw(เตะบอล)" {
		t.Fatalf("queries %s", got)
	}
	if len(hs.api.created) != 1 {
		t.Fatal("no event created")
	}
	p := hs.api.created[0]
	if p.Image != cover.DataURI([]byte("jpeg-auto")) {
		t.Fatalf("image %q", p.Image)
	}
	if want := "มาเตะกัน\n\n📷 “Auto” — Bob · CC0 · https://src/auto"; p.Description != want {
		t.Fatalf("description %q", p.Description)
	}

	ev := hs.sched.Upcoming(guildID)
	if len(ev) != 1 || hs.sched.Creator(ev[0]) != "42" {
		t.Fatal("event not tracked with its creator")
	}
	e := hs.lastEdit(t)
	embed := (*e.Embeds)[0]
	if !strings.Contains(embed.Title, "เตะบอล") || embed.Image == nil || !strings.Contains(embed.Image.URL, "/guild-events/"+ev[0].ID+"/") {
		t.Fatalf("embed %+v", embed)
	}
	start := time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok).Unix()
	if !strings.Contains(embed.Description, fmt.Sprintf("<t:%d:F>", start)) || !strings.Contains(embed.Description, fmt.Sprintf("<t:%d:R>", start)) {
		t.Fatalf("time %q", embed.Description)
	}
	if embed.Fields[0].Value != "🔊 <#777>" {
		t.Fatalf("place %q", embed.Fields[0].Value)
	}
	if ids := buttonIDs(*e.Components); len(ids) != 1 || ids[0] != recoverID(ev[0].ID) {
		t.Fatalf("buttons %v", ids)
	}
}

func TestCreate_WithoutCoverWhenNoneFound(t *testing.T) {
	hs := newHarness()
	hs.covers.autoErr = cover.ErrNoCover
	hs.h.HandleCommand(command("42", permManage, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y")))
	if len(hs.api.created) != 1 || hs.api.created[0].Image != "" || hs.api.created[0].Description != "" {
		t.Fatalf("created %+v", hs.api.created)
	}
	if e := hs.lastEdit(t); !strings.Contains(*e.Content, "หารูปปกไม่เจอ") {
		t.Fatalf("note %q", *e.Content)
	}
}

func TestCreate_UsesAttachment(t *testing.T) {
	hs := newHarness()
	res := &discordgo.ApplicationCommandInteractionDataResolved{Attachments: map[string]*discordgo.MessageAttachment{
		"a1": {ID: "a1", URL: "https://cdn/up.webp", ContentType: "image/webp", Size: 5000},
	}}
	hs.h.HandleCommand(commandWith("42", permManage, res, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"),
		strOpt("location", "y"), &discordgo.ApplicationCommandInteractionDataOption{Name: "image", Type: discordgo.ApplicationCommandOptionAttachment, Value: "a1"}))
	if len(hs.covers.fromURL) != 1 || len(hs.covers.queries) != 0 {
		t.Fatalf("fromURL %v queries %v", hs.covers.fromURL, hs.covers.queries)
	}
	if p := hs.api.created[0]; p.Image != cover.DataURI([]byte("jpeg-upload")) || p.Description != "" {
		t.Fatalf("params %+v", p)
	}
}

func TestCreate_DiscordError(t *testing.T) {
	hs := newHarness()
	hs.api.createErr = errors.New("HTTP 403 Missing Permissions")
	hs.h.HandleCommand(command("42", permManage, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y")))
	if e := hs.lastEdit(t); !strings.Contains(*e.Content, "Missing Permissions") {
		t.Fatalf("reply %q", *e.Content)
	}
}

// --- edit, cancel, list ---------------------------------------------------

func seed(hs *harness, id, creator string) *discordgo.GuildScheduledEvent {
	ev := externalEvent(id)
	ev.CreatorID = creator
	ev.ScheduledStartTime = time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok)
	ev.ScheduledEndTime = ptr(ev.ScheduledStartTime.Add(3 * time.Hour))
	ev.Description = "เดิม\n\n📷 Old · CC0 · https://old"
	ev = hs.api.add(ev)
	hs.sched.Upsert(ev)
	return ev
}

func TestEdit_Permissions(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("u", permCreate, "edit", strOpt("event", "5001"), strOpt("name", "ใหม่")))
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgNoManage {
		t.Fatalf("stranger: %q", got)
	}
	// Discord's creator may edit without Manage Events.
	hs.h.HandleCommand(command("77", 0, "edit", strOpt("event", "5001"), strOpt("name", "ใหม่")))
	if hs.api.get("5001").Name != "ใหม่" {
		t.Fatal("creator could not edit")
	}
	// So may the user who created a bot event through /event create.
	seed(hs, "5002", botID)
	hs.sched.SetCreator("5002", "88")
	hs.h.HandleCommand(command("88", 0, "edit", strOpt("event", "5002"), strOpt("name", "ของฉัน")))
	if hs.api.get("5002").Name != "ของฉัน" {
		t.Fatal("/event create user could not edit")
	}
}

func TestEdit_WhenKeepsDurationAndDescriptionKeepsCredit(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("u", permManage, "edit", strOpt("event", "5001"), strOpt("when", "เสาร์ 14.30"), strOpt("description", "ใหม่")))
	got := hs.api.get("5001")
	start := time.Date(2026, 11, 7, 14, 30, 0, 0, eventtime.Bangkok)
	if !got.ScheduledStartTime.Equal(start) || !got.ScheduledEndTime.Equal(start.Add(3*time.Hour)) {
		t.Fatalf("times %s – %s", got.ScheduledStartTime, got.ScheduledEndTime)
	}
	if got.Description != "ใหม่\n\n📷 Old · CC0 · https://old" {
		t.Fatalf("description %q", got.Description)
	}
	if e := hs.lastEdit(t); !strings.Contains(*e.Content, "แก้ไขแล้ว") {
		t.Fatalf("reply %q", *e.Content)
	}
	if r := hs.api.responses[0]; r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatal("edit reply is public")
	}
}

func TestParseEdit(t *testing.T) {
	voice := voiceEvent("1")
	voice.ScheduledStartTime = time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok)
	editOpts := func(opts ...*discordgo.ApplicationCommandInteractionDataOption) options {
		_, o := subcommand(command("u", 0, "edit", opts...).ApplicationCommandData())
		return o
	}

	// Moving a voice event without an end outside gives it the default end.
	p, err := parseEdit(voice, editOpts(strOpt("event", "1"), strOpt("location", "สวน")), now)
	if err != nil {
		t.Fatal(err)
	}
	if p.EntityType != discordgo.GuildScheduledEventEntityTypeExternal || p.EntityMetadata.Location != "สวน" ||
		p.ScheduledEndTime == nil || !p.ScheduledEndTime.Equal(voice.ScheduledStartTime.Add(2*time.Hour)) {
		t.Fatalf("params %+v", p)
	}
	// A moved voice event without an end stays without one.
	p, err = parseEdit(voice, editOpts(strOpt("event", "1"), strOpt("when", "พรุ่งนี้ 20:00")), now)
	if err != nil || p.ScheduledEndTime != nil {
		t.Fatalf("params %+v %v", p, err)
	}
	// end is read relative to the (new) start.
	p, err = parseEdit(voice, editOpts(strOpt("event", "1"), strOpt("end", "3 ชม.")), now)
	if err != nil || !p.ScheduledEndTime.Equal(voice.ScheduledStartTime.Add(3*time.Hour)) {
		t.Fatalf("end %+v %v", p, err)
	}
	p, err = parseEdit(voice, editOpts(strOpt("event", "1"), strOpt("place", "99")), now)
	if err != nil || p.EntityType != discordgo.GuildScheduledEventEntityTypeVoice || p.ChannelID != "99" {
		t.Fatalf("place %+v %v", p, err)
	}
	for label, opts := range map[string]options{
		"nothing":     editOpts(strOpt("event", "1")),
		"bad time":    editOpts(strOpt("event", "1"), strOpt("when", "เมื่อวาน")),
		"both places": editOpts(strOpt("event", "1"), strOpt("place", "9"), strOpt("location", "x")),
	} {
		if _, err := parseEdit(voice, opts, now); err == nil {
			t.Errorf("%s: want an error", label)
		}
	}
}

func TestFindEvent_ByIDOrName(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	if ev, err := hs.h.findEvent(guildID, "5001"); err != nil || ev.ID != "5001" {
		t.Fatalf("by ID: %v", err)
	}
	if ev, err := hs.h.findEvent(guildID, "เตะบอล"); err != nil || ev.ID != "5001" {
		t.Fatalf("by name: %v", err)
	}
	if _, err := hs.h.findEvent(guildID, "9999"); err == nil {
		t.Fatal("unknown ID found")
	}
	if _, err := hs.h.findEvent("other-guild", "5001"); err == nil {
		t.Fatal("found another guild's event")
	}
	seed(hs, "5002", "77") // same name
	if _, err := hs.h.findEvent(guildID, "เตะบอล"); err == nil {
		t.Fatal("ambiguous name accepted")
	}
}

func TestCancel_ConfirmFlow(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("77", 0, "cancel", strOpt("event", "5001")))
	r := hs.lastResponse(t)
	if r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatal("confirmation is public")
	}
	ids := buttonIDs(r.Data.Components)
	if len(ids) != 2 || ids[0] != cancelID("5001") || ids[1] != keepID("5001") {
		t.Fatalf("buttons %v", ids)
	}
	if hs.api.get("5001").Status != discordgo.GuildScheduledEventStatusScheduled {
		t.Fatal("canceled before confirming")
	}

	// A stranger pressing the button is refused.
	hs.h.HandleComponent(click("u", 0, cancelID("5001")))
	if hs.api.get("5001").Status != discordgo.GuildScheduledEventStatusScheduled {
		t.Fatal("stranger canceled the event")
	}
	hs.h.HandleComponent(click("u", 0, keepID("5001")))
	if r := hs.lastResponse(t); r.Type != discordgo.InteractionResponseUpdateMessage {
		t.Fatalf("keep: response %d", r.Type)
	}
	hs.h.HandleComponent(click("77", 0, cancelID("5001")))
	if hs.api.get("5001").Status != discordgo.GuildScheduledEventStatusCanceled {
		t.Fatal("not canceled")
	}
	if len(hs.sched.Upcoming(guildID)) != 0 {
		t.Fatal("canceled event still tracked")
	}
}

func TestCancel_ActiveEventIsEnded(t *testing.T) {
	hs := newHarness()
	ev := seed(hs, "5001", "77")
	ev.Status = discordgo.GuildScheduledEventStatusActive
	hs.api.add(ev)
	hs.h.HandleComponent(click("u", permManage, cancelID("5001")))
	if hs.api.get("5001").Status != discordgo.GuildScheduledEventStatusCompleted {
		t.Fatalf("status %d", hs.api.get("5001").Status)
	}
}

func TestList(t *testing.T) {
	hs := newHarness()
	seed(hs, "5001", "77")
	soon := externalEvent("5000")
	soon.ScheduledStartTime = now.Add(time.Hour)
	soon.UserCount = 4
	hs.api.add(soon)
	done := externalEvent("4999")
	done.Status = discordgo.GuildScheduledEventStatusCompleted
	hs.api.add(done)
	hs.h.HandleCommand(command("u", 0, "list"))
	r := hs.lastResponse(t)
	desc := r.Data.Embeds[0].Description
	if strings.Count(desc, "https://discord.com/events/") != 2 || strings.Index(desc, "5000") > strings.Index(desc, "5001") {
		t.Fatalf("list %q", desc)
	}
	if !strings.Contains(desc, "👥 4") || !strings.Contains(desc, "📍 สนาม") {
		t.Fatalf("list %q", desc)
	}
	if len(hs.sched.Upcoming(guildID)) != 2 {
		t.Fatal("list did not sync the scheduler")
	}
	if ListEmbed(nil).Description == "" {
		t.Fatal("empty list has no text")
	}
}

func TestGuildOnly(t *testing.T) {
	hs := newHarness()
	i := command("u", permManage, "list")
	i.GuildID, i.Member = "", nil
	i.User = &discordgo.User{ID: "u"}
	hs.h.HandleCommand(i)
	if got := ephemeralText(t, hs.lastResponse(t)); got != msgGuildOnly {
		t.Fatalf("reply %q", got)
	}
}

// Moving an external event into a voice channel must send entity_metadata:
// null, which discordgo omits, so it goes through the raw PATCH.
func TestEdit_ExternalToVoiceClearsEntityMetadata(t *testing.T) {
	hs := newHarness()
	seed(hs, "5002", "77")
	hs.h.HandleCommand(command("u", permManage, "edit", strOpt("event", "5002"), strOpt("place", "888")))
	if len(hs.api.rawPatches) != 1 {
		t.Fatalf("raw patches: %d, want 1", len(hs.api.rawPatches))
	}
	body := hs.api.rawPatches[0]
	if v, ok := body["entity_metadata"]; !ok || v != nil {
		t.Fatalf("entity_metadata = %v (present %v), want explicit null", v, ok)
	}
	if _, ok := body["description"]; ok && body["description"] == nil {
		t.Fatal("description was cleared on a place-only edit")
	}
}
