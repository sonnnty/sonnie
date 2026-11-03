package events

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/eventtime"
)

// CoverFinder is what the handler needs from *cover.Finder.
type CoverFinder interface {
	Query(ctx context.Context, name, keywords string) string
	Search(ctx context.Context, query string, page, n int) ([]cover.Candidate, error)
	Fetch(ctx context.Context, c cover.Candidate) (cover.Cover, error)
	Auto(ctx context.Context, query string) (cover.Cover, error)
	FromURL(ctx context.Context, url string) (cover.Cover, error)
}

var _ CoverFinder = (*cover.Finder)(nil)

// workTimeout bounds the slow part of a command (keywords, search,
// download, Discord); the interaction token itself lasts 15 minutes.
const workTimeout = 40 * time.Second

// Handler answers /event, its autocomplete and its buttons.
type Handler struct {
	api     API
	sched   *Scheduler
	covers  CoverFinder
	now     func() time.Time
	pickers *pickerStore
}

func NewHandler(api API, sched *Scheduler, covers CoverFinder, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{api: api, sched: sched, covers: covers, now: now, pickers: newPickerStore(now)}
}

// caller is who sent an interaction.
type caller struct {
	userID string
	perms  int64
}

func callerOf(i *discordgo.InteractionCreate) (caller, bool) {
	if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
		return caller{}, false
	}
	return caller{userID: i.Member.User.ID, perms: i.Member.Permissions}, true
}

const (
	msgGuildOnly  = "/event ใช้ได้เฉพาะในเซิร์ฟเวอร์"
	msgNoCreate   = "🚫 สร้างอีเวนต์ได้เฉพาะคนที่มีสิทธิ์ Create Events หรือ Manage Events"
	msgNoManage   = "🚫 แก้ไขได้เฉพาะคนที่สร้างอีเวนต์นี้ หรือคนที่มีสิทธิ์ Manage Events"
	msgNotFound   = "ไม่พบอีเวนต์นี้ — เลือกจากรายการที่ขึ้นตอนพิมพ์"
	msgBadButton  = "ปุ่มนี้ใช้ไม่ได้แล้ว"
	msgPlaceOrLoc = "ระบุ `place` (ห้องเสียง) หรือ `location` (สถานที่/ลิงก์) อย่างใดอย่างหนึ่ง"
)

// HandleCommand answers /event.
func (h *Handler) HandleCommand(i *discordgo.InteractionCreate) {
	who, ok := callerOf(i)
	if !ok {
		h.ephemeral(i, msgGuildOnly)
		return
	}
	sub, opts := subcommand(i.ApplicationCommandData())
	switch sub {
	case "create":
		h.create(i, who, opts)
	case "cover":
		h.cover(i, who, opts)
	case "edit":
		h.edit(i, who, opts)
	case "cancel":
		h.cancel(i, who, opts)
	case "list":
		h.list(i)
	}
}

func (h *Handler) respond(i *discordgo.InteractionCreate, t discordgo.InteractionResponseType, data *discordgo.InteractionResponseData) error {
	err := h.api.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: t, Data: data})
	if err != nil {
		log.Printf("events: respond to interaction: %v", err)
	}
	return err
}

func (h *Handler) ephemeral(i *discordgo.InteractionCreate, content string) {
	h.respond(i, discordgo.InteractionResponseChannelMessageWithSource,
		&discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral})
}

func (h *Handler) ephemeralEmbed(i *discordgo.InteractionCreate, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) {
	h.respond(i, discordgo.InteractionResponseChannelMessageWithSource, &discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{e}, Components: comps, Flags: discordgo.MessageFlagsEphemeral,
	})
}

// defer acknowledges now and lets the reply be edited later.
func (h *Handler) deferReply(i *discordgo.InteractionCreate, ephemeral bool) bool {
	data := &discordgo.InteractionResponseData{}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return h.respond(i, discordgo.InteractionResponseDeferredChannelMessageWithSource, data) == nil
}

// editReply replaces the deferred reply or the message a button is on.
func (h *Handler) editReply(i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, comps []discordgo.MessageComponent) {
	if embeds == nil {
		embeds = []*discordgo.MessageEmbed{}
	}
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	if _, err := h.api.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content, Embeds: &embeds, Components: &comps,
	}); err != nil {
		log.Printf("events: edit interaction reply: %v", err)
	}
}

// createRequest is a validated /event create.
type createRequest struct {
	params     discordgo.GuildScheduledEventParams
	body       string
	attachment *discordgo.MessageAttachment
	keywords   string
}

// parseCreate validates the options of /event create. The error is shown
// to the user.
func parseCreate(opts options, now time.Time) (createRequest, error) {
	name := opts.str("name")
	if name == "" {
		return createRequest{}, errors.New("ใส่ชื่ออีเวนต์ด้วย")
	}
	start, err := eventtime.ParseStart(opts.str("when"), now)
	if err != nil {
		return createRequest{}, fmt.Errorf("เวลาเริ่ม: %w", err)
	}
	end := start.Add(eventtime.DefaultDuration)
	if s := opts.str("end"); s != "" {
		if end, err = eventtime.ParseEnd(s, start, now); err != nil {
			return createRequest{}, fmt.Errorf("เวลาจบ: %w", err)
		}
	}
	req := createRequest{
		params: discordgo.GuildScheduledEventParams{
			Name:               truncate(name, maxNameLen),
			ScheduledStartTime: &start,
			ScheduledEndTime:   &end,
			PrivacyLevel:       discordgo.GuildScheduledEventPrivacyLevelGuildOnly,
		},
		body:     truncate(opts.str("description"), maxUserDescription),
		keywords: opts.str("keywords"),
	}
	if err := setPlace(&req.params, opts.str("place"), opts.str("location")); err != nil {
		return createRequest{}, err
	}
	if req.params.EntityType == 0 {
		return createRequest{}, errors.New(msgPlaceOrLoc)
	}
	if req.attachment, err = imageAttachment(opts); err != nil {
		return createRequest{}, err
	}
	return req, nil
}

// setPlace makes it a voice event in channelID, or an external event at
// location. Neither leaves p alone.
func setPlace(p *discordgo.GuildScheduledEventParams, channelID, location string) error {
	switch {
	case channelID != "" && location != "":
		return errors.New(msgPlaceOrLoc)
	case channelID != "":
		p.EntityType = discordgo.GuildScheduledEventEntityTypeVoice
		p.ChannelID = channelID
	case location != "":
		p.EntityType = discordgo.GuildScheduledEventEntityTypeExternal
		p.ChannelID = "" // sent as null
		p.EntityMetadata = &discordgo.GuildScheduledEventEntityMetadata{Location: truncate(location, maxLocationLen)}
	}
	return nil
}

var imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

func imageAttachment(opts options) (*discordgo.MessageAttachment, error) {
	if opts.str("image") == "" {
		return nil, nil
	}
	a := opts.attachment("image")
	if a == nil {
		return nil, errors.New("อ่านไฟล์แนบไม่ได้ ลองแนบใหม่")
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(a.ContentType, ";", 2)[0]))
	if !imageTypes[ct] {
		return nil, errors.New("รูปปกต้องเป็น JPEG, PNG หรือ WebP")
	}
	if a.Size > cover.MaxDownloadBytes {
		return nil, errors.New("รูปปกต้องไม่เกิน 10 MB")
	}
	return a, nil
}

func (h *Handler) create(i *discordgo.InteractionCreate, who caller, opts options) {
	if !canCreate(who.perms) {
		h.ephemeral(i, msgNoCreate)
		return
	}
	req, err := parseCreate(opts, h.now())
	if err != nil {
		h.ephemeral(i, err.Error())
		return
	}
	// Finding a cover takes longer than the 3 s Discord allows.
	if !h.deferReply(i, false) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTimeout)
	defer cancel()

	var cov cover.Cover
	note := ""
	if req.attachment != nil {
		cov, err = h.covers.FromURL(ctx, req.attachment.URL)
		if err != nil {
			log.Printf("events: attached cover %s: %v", req.attachment.URL, err)
			note = "⚠️ ใช้รูปที่แนบไม่ได้ — สร้างโดยไม่มีรูปปก"
		}
	} else {
		query := h.covers.Query(ctx, req.params.Name, req.keywords)
		cov, err = h.covers.Auto(ctx, query)
		if err != nil {
			log.Printf("events: cover for %q (search %q): %v", req.params.Name, query, err)
			note = "⚠️ หารูปปกไม่เจอ — กด 🔄 เปลี่ยนรูป เพื่อค้นใหม่"
		}
	}
	if len(cov.JPEG) > 0 {
		req.params.Image = cover.DataURI(cov.JPEG)
	}
	if d := joinCredit(req.body, cov.Credit); d != "" {
		req.params.Description = d
	}

	ev, err := h.api.GuildScheduledEventCreate(i.GuildID, &req.params)
	if err != nil {
		log.Printf("events: create %q in guild %s: %v", req.params.Name, i.GuildID, err)
		h.editReply(i, "สร้างอีเวนต์ไม่สำเร็จ: "+apiError(err), nil, nil)
		return
	}
	log.Printf("events: %s created event %s (%q)", who.userID, ev.ID, ev.Name)
	h.sched.SetCreator(ev.ID, who.userID)
	h.sched.Upsert(ev)
	h.editReply(i, note, []*discordgo.MessageEmbed{EventEmbed(ev)}, eventButtons(ev))
}

// findEvent resolves the event option: an ID picked from autocomplete, or
// a name typed without picking.
func (h *Handler) findEvent(guildID, value string) (*discordgo.GuildScheduledEvent, error) {
	value = strings.TrimSpace(value)
	if isSnowflake(value) {
		ev, err := h.api.GuildScheduledEvent(guildID, value, false)
		if err == nil && ev != nil && ev.GuildID == guildID && upcoming(ev) {
			return ev, nil
		}
	}
	var match *discordgo.GuildScheduledEvent
	for _, ev := range h.sched.Upcoming(guildID) {
		if strings.EqualFold(ev.Name, value) {
			if match != nil {
				return nil, errors.New("มีหลายอีเวนต์ชื่อนี้ — เลือกจากรายการที่ขึ้นตอนพิมพ์")
			}
			match = ev
		}
	}
	if match == nil {
		return nil, errors.New(msgNotFound)
	}
	return match, nil
}

// managedEvent finds the event option and checks the caller may change it.
// It answers the interaction itself when not.
func (h *Handler) managedEvent(i *discordgo.InteractionCreate, who caller, value string) (*discordgo.GuildScheduledEvent, bool) {
	ev, err := h.findEvent(i.GuildID, value)
	if err != nil {
		h.ephemeral(i, err.Error())
		return nil, false
	}
	if !canManage(who.perms, who.userID, h.sched.Creator(ev)) {
		h.ephemeral(i, msgNoManage)
		return nil, false
	}
	return ev, true
}

// parseEdit builds the changes /event edit asks for.
func parseEdit(ev *discordgo.GuildScheduledEvent, opts options, now time.Time) (*discordgo.GuildScheduledEventParams, error) {
	p := &discordgo.GuildScheduledEventParams{}
	changed := false
	if name := opts.str("name"); name != "" {
		p.Name = truncate(name, maxNameLen)
		changed = true
	}

	start := ev.ScheduledStartTime
	if s := opts.str("when"); s != "" {
		t, err := eventtime.ParseStart(s, now)
		if err != nil {
			return nil, fmt.Errorf("เวลาเริ่ม: %w", err)
		}
		p.ScheduledStartTime = &t
		start = t
		changed = true
		if ev.ScheduledEndTime != nil {
			// Keep the length of the event.
			end := t.Add(ev.ScheduledEndTime.Sub(ev.ScheduledStartTime))
			p.ScheduledEndTime = &end
		}
	}
	if s := opts.str("end"); s != "" {
		t, err := eventtime.ParseEnd(s, start, now)
		if err != nil {
			return nil, fmt.Errorf("เวลาจบ: %w", err)
		}
		p.ScheduledEndTime = &t
		changed = true
	}

	if err := setPlace(p, opts.str("place"), opts.str("location")); err != nil {
		return nil, err
	}
	if p.EntityType != 0 {
		changed = true
		if p.EntityType == discordgo.GuildScheduledEventEntityTypeExternal && p.ScheduledEndTime == nil {
			// External events need an end.
			end := start.Add(eventtime.DefaultDuration)
			if ev.ScheduledEndTime != nil {
				end = *ev.ScheduledEndTime
			}
			p.ScheduledEndTime = &end
		}
	}

	if d := opts.str("description"); d != "" {
		_, credit := splitCredit(ev.Description)
		p.Description = joinCredit(truncate(d, maxUserDescription), credit)
		changed = true
	}
	if !changed {
		return nil, errors.New("ไม่มีอะไรให้แก้ — ใส่อย่างน้อยหนึ่งช่อง เช่น `when` หรือ `name`")
	}
	return p, nil
}

func (h *Handler) edit(i *discordgo.InteractionCreate, who caller, opts options) {
	ev, ok := h.managedEvent(i, who, opts.str("event"))
	if !ok {
		return
	}
	p, err := parseEdit(ev, opts, h.now())
	if err != nil {
		h.ephemeral(i, err.Error())
		return
	}
	if !h.deferReply(i, true) {
		return
	}
	updated, err := h.editEvent(ev, p, false)
	if err != nil {
		log.Printf("events: edit event %s: %v", ev.ID, err)
		h.editReply(i, "แก้ไขไม่สำเร็จ: "+apiError(err), nil, nil)
		return
	}
	log.Printf("events: %s edited event %s (%q)", who.userID, ev.ID, updated.Name)
	h.sched.Upsert(updated)
	h.editReply(i, "✅ แก้ไขแล้ว", []*discordgo.MessageEmbed{EventEmbed(updated)}, eventButtons(updated))
}

func (h *Handler) cancel(i *discordgo.InteractionCreate, who caller, opts options) {
	ev, ok := h.managedEvent(i, who, opts.str("event"))
	if !ok {
		return
	}
	e := EventEmbed(ev)
	e.Title = truncate("ยกเลิก "+ev.Name+" ?", 256)
	e.Color = colorDanger
	e.Image = nil
	comps := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "ยกเลิกอีเวนต์", Style: discordgo.DangerButton, CustomID: cancelID(ev.ID)},
		discordgo.Button{Label: "ไม่ยกเลิก", Style: discordgo.SecondaryButton, CustomID: keepID(ev.ID)},
	}}}
	h.ephemeralEmbed(i, e, comps)
}

// confirmCancel runs when "ยกเลิกอีเวนต์" is pressed. A scheduled event is
// canceled; one that is already running is ended (Discord allows only
// ACTIVE -> COMPLETED).
func (h *Handler) confirmCancel(i *discordgo.InteractionCreate, who caller, eventID string) {
	ev, err := h.findEvent(i.GuildID, eventID)
	if err != nil || ev.ID != eventID {
		h.update(i, msgNotFound, nil, nil)
		return
	}
	if !canManage(who.perms, who.userID, h.sched.Creator(ev)) {
		h.ephemeral(i, msgNoManage)
		return
	}
	status := discordgo.GuildScheduledEventStatusCanceled
	if ev.Status == discordgo.GuildScheduledEventStatusActive {
		status = discordgo.GuildScheduledEventStatusCompleted
	}
	updated, err := h.api.GuildScheduledEventEdit(i.GuildID, ev.ID, &discordgo.GuildScheduledEventParams{Status: status})
	if err != nil {
		log.Printf("events: cancel event %s: %v", ev.ID, err)
		h.update(i, "ยกเลิกไม่สำเร็จ: "+apiError(err), nil, nil)
		return
	}
	log.Printf("events: %s canceled event %s (%q)", who.userID, ev.ID, ev.Name)
	if updated == nil {
		h.sched.Remove(ev.ID)
	} else {
		h.sched.Upsert(updated)
	}
	h.update(i, "", []*discordgo.MessageEmbed{okEmbed(truncate("🗑️ ยกเลิก "+ev.Name+" แล้ว", 256))}, nil)
}

// update replaces the message a button is on, as the response to the click.
func (h *Handler) update(i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, comps []discordgo.MessageComponent) {
	if embeds == nil {
		embeds = []*discordgo.MessageEmbed{}
	}
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	h.respond(i, discordgo.InteractionResponseUpdateMessage, &discordgo.InteractionResponseData{
		Content: content, Embeds: embeds, Components: comps,
	})
}

func (h *Handler) list(i *discordgo.InteractionCreate) {
	evs, err := h.api.GuildScheduledEvents(i.GuildID, true)
	if err != nil {
		log.Printf("events: list guild %s: %v", i.GuildID, err)
		h.ephemeral(i, "อ่านรายการอีเวนต์ไม่สำเร็จ: "+apiError(err))
		return
	}
	h.sched.Sync(i.GuildID, evs)
	var up []*discordgo.GuildScheduledEvent
	for _, ev := range evs {
		if upcoming(ev) {
			up = append(up, ev)
		}
	}
	sortByStart(up)
	h.ephemeralEmbed(i, ListEmbed(up), nil)
}
