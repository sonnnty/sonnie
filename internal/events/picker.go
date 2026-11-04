package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
)

const (
	// pickerSize is how many candidates the picker shows (buttons 1-5).
	pickerSize = 5
	// pickerTTL matches the life of an interaction token.
	pickerTTL = 15 * time.Minute
	// maxAutocomplete is Discord's limit on choices.
	maxAutocomplete = 25
)

// picker is one open cover picker: the candidates shown to one user for
// one event. It lives in memory; after a restart its buttons say so.
type picker struct {
	token   string
	guildID string
	eventID string
	userID  string
	query   string
	page    int
	cands   []cover.Candidate
	expires time.Time
}

type pickerStore struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]picker
}

func newPickerStore(now func() time.Time) *pickerStore {
	return &pickerStore{now: now, m: map[string]picker{}}
}

// put stores p under a new random token and drops expired pickers.
func (s *pickerStore) put(p picker) string {
	var b [pickerTokenLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	p.token = hex.EncodeToString(b[:])
	now := s.now()
	p.expires = now.Add(pickerTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[p.token] = p
	return p.token
}

func (s *pickerStore) get(token string) (picker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[token]
	if ok && s.now().After(p.expires) {
		delete(s.m, token)
		return picker{}, false
	}
	return p, ok
}

func (s *pickerStore) delete(token string) {
	s.mu.Lock()
	delete(s.m, token)
	s.mu.Unlock()
}

// pickerMessage shows each candidate as an embed with its picture, and the
// buttons 1-5 and 🔎 ค้นใหม่ (next page).
func pickerMessage(p picker, eventName, note string) (string, []*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	content := fmt.Sprintf("เลือกรูปปกสำหรับ **%s** · ค้น `%s` · หน้า %d", truncate(eventName, 80), truncate(p.query, 60), p.page)
	if note != "" {
		content = note + "\n" + content
	}
	var embeds []*discordgo.MessageEmbed
	var buttons []discordgo.MessageComponent
	for i, c := range p.cands {
		n := i + 1
		desc := strings.TrimPrefix(c.Credit(), cover.CreditPrefix)
		embeds = append(embeds, &discordgo.MessageEmbed{
			Title:       fmt.Sprintf("%d", n),
			URL:         c.SourceURL,
			Description: truncate(desc, 200),
			Color:       colorEvent,
			Image:       &discordgo.MessageEmbedImage{URL: c.ThumbURL},
		})
		buttons = append(buttons, discordgo.Button{Label: fmt.Sprint(n), Style: discordgo.PrimaryButton, CustomID: pickID(p.token, p.eventID, n)})
	}
	comps := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: buttons},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "ค้นใหม่", Emoji: &discordgo.ComponentEmoji{Name: "🔎"}, Style: discordgo.SecondaryButton, CustomID: moreID(p.token, p.eventID)},
		}},
	}
	return content, embeds, comps
}

func (h *Handler) cover(i *discordgo.InteractionCreate, who caller, opts options) {
	ev, ok := h.managedEvent(i, who, "cover", opts.str("event"))
	if !ok {
		return
	}
	att, err := imageAttachment(opts)
	if err != nil {
		h.reject(i, who, "cover", err)
		return
	}
	if !h.deferReply(i, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTimeout)
	defer cancel()
	if att != nil {
		cov, err := h.covers.FromURL(ctx, att.URL)
		if err != nil {
			log.Printf("events: attached cover %s: %v", att.URL, err)
			h.editReply(i, "ใช้รูปที่แนบไม่ได้ — ต้องเป็น JPEG, PNG หรือ WebP ไม่เกิน 10 MB", nil, nil)
			return
		}
		h.applyCover(i, who, ev, cov)
		return
	}
	query := h.covers.Query(ctx, ev.Name, opts.str("keywords"))
	h.showPicker(ctx, i, who, ev, query, 1, "")
}

// showPicker searches page and puts the picker in the deferred reply (or
// the message the clicked button is on).
func (h *Handler) showPicker(ctx context.Context, i *discordgo.InteractionCreate, who caller, ev *discordgo.GuildScheduledEvent, query string, page int, note string) {
	cands, err := h.covers.Search(ctx, query, page, pickerSize)
	if len(cands) == 0 && page > 1 {
		page = 1
		note = "ไม่มีรูปเพิ่มแล้ว — กลับไปหน้าแรก"
		cands, err = h.covers.Search(ctx, query, page, pickerSize)
	}
	if len(cands) == 0 {
		if err != nil {
			log.Printf("events: cover search %q: %v", query, err)
		}
		h.editReply(i, fmt.Sprintf("ไม่พบรูปสำหรับ `%s` — ลองใส่ `keywords` อื่น หรือแนบรูปเองด้วย `/event cover`", truncate(query, 60)), nil, nil)
		return
	}
	if len(cands) > pickerSize {
		cands = cands[:pickerSize]
	}
	p := picker{guildID: ev.GuildID, eventID: ev.ID, userID: who.userID, query: query, page: page, cands: cands}
	p.token = h.pickers.put(p)
	content, embeds, comps := pickerMessage(p, ev.Name, note)
	h.editReply(i, content, embeds, comps)
}

// applyCover sets the cover and replaces the credit line, then shows the
// event in the reply.
func (h *Handler) applyCover(i *discordgo.InteractionCreate, who caller, ev *discordgo.GuildScheduledEvent, cov cover.Cover) {
	body, _ := splitCredit(ev.Description)
	desc := joinCredit(body, cov.Credit)
	p := &discordgo.GuildScheduledEventParams{Image: cover.DataURI(cov.JPEG)}
	clearDesc := false
	if desc != ev.Description {
		// discordgo omits an empty description, so clearing needs a raw request.
		clearDesc = desc == ""
		p.Description = desc
	}
	updated, err := h.editEvent(ev, p, clearDesc)
	if err != nil {
		log.Printf("events: set cover of %s: %v", ev.ID, err)
		h.editReply(i, "เปลี่ยนรูปปกไม่สำเร็จ: "+apiError(err), nil, nil)
		return
	}
	log.Printf("events: %s changed the cover of %s (%q)", who.userID, ev.ID, ev.Name)
	h.sched.Upsert(updated)
	h.editReply(i, "✅ เปลี่ยนรูปปกแล้ว", []*discordgo.MessageEmbed{EventEmbed(updated)}, nil)
}

func (h *Handler) editEvent(ev *discordgo.GuildScheduledEvent, p *discordgo.GuildScheduledEventParams, clearDescription bool) (*discordgo.GuildScheduledEvent, error) {
	// Discord needs entity_metadata: null when an external event becomes a
	// voice event; discordgo omits the field, so that change goes raw too.
	toVoice := ev.EntityType == discordgo.GuildScheduledEventEntityTypeExternal &&
		p.EntityType == discordgo.GuildScheduledEventEntityTypeVoice
	if !clearDescription && !toVoice {
		return h.api.GuildScheduledEventEdit(ev.GuildID, ev.ID, p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if clearDescription {
		body["description"] = nil
	}
	if toVoice {
		body["entity_metadata"] = nil
	}
	endpoint := discordgo.EndpointGuildScheduledEvent(ev.GuildID, ev.ID)
	resp, err := h.api.RequestWithBucketID("PATCH", endpoint, body, endpoint)
	if err != nil {
		return nil, err
	}
	var out discordgo.GuildScheduledEvent
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HandleComponent answers the buttons of /event.
func (h *Handler) HandleComponent(i *discordgo.InteractionCreate) {
	who, ok := callerOf(i)
	if !ok {
		h.ephemeral(i, msgGuildOnly)
		return
	}
	id, err := parseCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		h.ephemeral(i, msgBadButton)
		return
	}
	switch id.kind {
	case kindPick:
		h.pick(i, who, id)
	case kindMore:
		h.more(i, who, id)
	case kindRecover:
		h.changeCover(i, who, id.eventID)
	case kindCancel:
		h.confirmCancel(i, who, id.eventID)
	case kindKeep:
		h.update(i, "", []*discordgo.MessageEmbed{okEmbed("ไม่ได้ยกเลิก")}, nil)
	}
}

// openPicker checks a picker button: the picker exists, belongs to this
// event and to the user who clicked, and the user may still change the
// event. It answers the interaction itself when not.
func (h *Handler) openPicker(i *discordgo.InteractionCreate, who caller, id customID) (picker, *discordgo.GuildScheduledEvent, bool) {
	p, ok := h.pickers.get(id.picker)
	if !ok {
		h.update(i, "หมดเวลาเลือกรูปแล้ว — ใช้ `/event cover` อีกครั้ง", nil, nil)
		return picker{}, nil, false
	}
	if p.eventID != id.eventID || p.guildID != i.GuildID {
		h.ephemeral(i, msgBadButton)
		return picker{}, nil, false
	}
	if p.userID != who.userID {
		h.ephemeral(i, "🚫 ตัวเลือกนี้เป็นของคนที่เปิดมัน")
		return picker{}, nil, false
	}
	if id.kind == kindPick && id.choice > len(p.cands) {
		h.ephemeral(i, msgBadButton)
		return picker{}, nil, false
	}
	ev, err := h.findEvent(i.GuildID, id.eventID)
	if err != nil || ev.ID != id.eventID {
		h.pickers.delete(p.token)
		h.update(i, msgNotFound, nil, nil)
		return picker{}, nil, false
	}
	if !canManage(who.perms, who.userID, h.sched.Creator(ev)) {
		h.ephemeral(i, msgNoManage)
		return picker{}, nil, false
	}
	return p, ev, true
}

func (h *Handler) pick(i *discordgo.InteractionCreate, who caller, id customID) {
	p, ev, ok := h.openPicker(i, who, id)
	if !ok {
		return
	}
	if h.respond(i, discordgo.InteractionResponseDeferredMessageUpdate, nil) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTimeout)
	defer cancel()
	c := p.cands[id.choice-1]
	cov, err := h.covers.Fetch(ctx, c)
	if err != nil {
		log.Printf("events: cover %s/%s: %v", c.Provider, c.ID, err)
		content, embeds, comps := pickerMessage(p, ev.Name, fmt.Sprintf("⚠️ โหลดรูปที่ %d ไม่ได้ — ลองรูปอื่น", id.choice))
		h.editReply(i, content, embeds, comps)
		return
	}
	h.pickers.delete(p.token)
	h.applyCover(i, who, ev, cov)
}

func (h *Handler) more(i *discordgo.InteractionCreate, who caller, id customID) {
	p, ev, ok := h.openPicker(i, who, id)
	if !ok {
		return
	}
	if h.respond(i, discordgo.InteractionResponseDeferredMessageUpdate, nil) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTimeout)
	defer cancel()
	h.pickers.delete(p.token)
	h.showPicker(ctx, i, who, ev, p.query, p.page+1, "")
}

// changeCover opens a picker from the 🔄 button on a /event create reply. That
// reply is public, so the picker is a new ephemeral message.
func (h *Handler) changeCover(i *discordgo.InteractionCreate, who caller, eventID string) {
	ev, err := h.findEvent(i.GuildID, eventID)
	if err != nil || ev.ID != eventID {
		h.ephemeral(i, msgNotFound)
		return
	}
	if !canManage(who.perms, who.userID, h.sched.Creator(ev)) {
		h.ephemeral(i, msgNoManage)
		return
	}
	if !h.deferReply(i, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workTimeout)
	defer cancel()
	query := h.covers.Query(ctx, ev.Name, "")
	h.showPicker(ctx, i, who, ev, query, 1, "")
}

// HandleAutocomplete suggests upcoming events for the event option.
func (h *Handler) HandleAutocomplete(i *discordgo.InteractionCreate) {
	_, opts := subcommand(i.ApplicationCommandData())
	typed := ""
	if opt, ok := opts.focused(); ok {
		typed, _ = opt.Value.(string)
	}
	evs := h.sched.Upcoming(i.GuildID)
	if len(evs) == 0 {
		// Not loaded yet (the guild arrived moments ago): ask Discord.
		if err := h.sched.LoadGuild(i.GuildID); err != nil {
			log.Printf("events: autocomplete: %v", err)
		}
		evs = h.sched.Upcoming(i.GuildID)
	}
	choices := autocompleteChoices(evs, typed)
	h.respond(i, discordgo.InteractionApplicationCommandAutocompleteResult, &discordgo.InteractionResponseData{Choices: choices})
}

func autocompleteChoices(evs []*discordgo.GuildScheduledEvent, typed string) []*discordgo.ApplicationCommandOptionChoice {
	typed = strings.ToLower(strings.TrimSpace(typed))
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	for _, ev := range evs {
		if typed != "" && !strings.Contains(strings.ToLower(ev.Name), typed) {
			continue
		}
		label := ShortTime(ev.ScheduledStartTime) + " · " + ev.Name
		if ev.Status == discordgo.GuildScheduledEventStatusActive {
			label = "🟢 " + label
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncate(label, 100), Value: ev.ID})
		if len(choices) == maxAutocomplete {
			break
		}
	}
	return choices
}
