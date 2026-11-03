package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// fakeAPI is an in-memory Discord: events, interested users, sent messages
// and interaction responses.
type fakeAPI struct {
	mu sync.Mutex

	events    map[string]*discordgo.GuildScheduledEvent
	users     map[string][]*discordgo.GuildScheduledEventUser
	nextID    int
	editErr   error
	createErr error
	listErr   error
	sendErr   error

	created    []*discordgo.GuildScheduledEventParams
	edits      []editCall
	sent       []sentMessage
	responses  []*discordgo.InteractionResponse
	webEdits   []*discordgo.WebhookEdit
	rawPatches []map[string]any
}

type editCall struct {
	eventID string
	params  *discordgo.GuildScheduledEventParams
}

type sentMessage struct {
	channelID string
	msg       *discordgo.MessageSend
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{events: map[string]*discordgo.GuildScheduledEvent{}, users: map[string][]*discordgo.GuildScheduledEventUser{}, nextID: 1000}
}

func (f *fakeAPI) add(ev *discordgo.GuildScheduledEvent) *discordgo.GuildScheduledEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *ev
	f.events[ev.ID] = &cp
	return &cp
}

func (f *fakeAPI) get(id string) *discordgo.GuildScheduledEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ev, ok := f.events[id]; ok {
		cp := *ev
		return &cp
	}
	return nil
}

func (f *fakeAPI) GuildScheduledEvents(guildID string, _ bool, _ ...discordgo.RequestOption) ([]*discordgo.GuildScheduledEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*discordgo.GuildScheduledEvent
	for _, ev := range f.events {
		if ev.GuildID == guildID {
			cp := *ev
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeAPI) GuildScheduledEvent(guildID, eventID string, _ bool, _ ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.events[eventID]
	if !ok || ev.GuildID != guildID {
		return nil, errors.New("HTTP 404 Not Found")
	}
	cp := *ev
	return &cp, nil
}

func (f *fakeAPI) GuildScheduledEventCreate(guildID string, p *discordgo.GuildScheduledEventParams, _ ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, p)
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.nextID++
	ev := &discordgo.GuildScheduledEvent{
		ID: fmt.Sprint(f.nextID), GuildID: guildID, ChannelID: p.ChannelID, CreatorID: botID,
		Name: p.Name, Description: p.Description, ScheduledStartTime: *p.ScheduledStartTime, ScheduledEndTime: p.ScheduledEndTime,
		Status: discordgo.GuildScheduledEventStatusScheduled, EntityType: p.EntityType,
	}
	if p.EntityMetadata != nil {
		ev.EntityMetadata = *p.EntityMetadata
	}
	if p.Image != "" {
		ev.Image = "hash" + ev.ID
	}
	f.events[ev.ID] = ev
	cp := *ev
	return &cp, nil
}

func (f *fakeAPI) GuildScheduledEventEdit(guildID, eventID string, p *discordgo.GuildScheduledEventParams, _ ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, editCall{eventID, p})
	if f.editErr != nil {
		return nil, f.editErr
	}
	ev, ok := f.events[eventID]
	if !ok || ev.GuildID != guildID {
		return nil, errors.New("HTTP 404 Not Found")
	}
	if p.Name != "" {
		ev.Name = p.Name
	}
	if p.Description != "" {
		ev.Description = p.Description
	}
	if p.ScheduledStartTime != nil {
		ev.ScheduledStartTime = *p.ScheduledStartTime
	}
	if p.ScheduledEndTime != nil {
		ev.ScheduledEndTime = p.ScheduledEndTime
	}
	if p.Status != 0 {
		ev.Status = p.Status
	}
	if p.EntityType != 0 {
		ev.EntityType = p.EntityType
		ev.ChannelID = p.ChannelID
	}
	if p.EntityMetadata != nil {
		ev.EntityMetadata = *p.EntityMetadata
	}
	if p.Image != "" {
		ev.Image = fmt.Sprintf("hash%s-%d", ev.ID, len(f.edits))
	}
	cp := *ev
	return &cp, nil
}

func (f *fakeAPI) GuildScheduledEventUsers(_, eventID string, limit int, _ bool, _, afterID string, _ ...discordgo.RequestOption) ([]*discordgo.GuildScheduledEventUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*discordgo.GuildScheduledEventUser
	for _, u := range f.users[eventID] {
		if afterID != "" && !idLess(afterID, u.User.ID) {
			continue
		}
		out = append(out, u)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func idLess(a, b string) bool {
	return len(a) < len(b) || (len(a) == len(b) && a < b)
}

func (f *fakeAPI) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, sentMessage{channelID, data})
	return &discordgo.Message{ID: "m", ChannelID: channelID}, nil
}

func (f *fakeAPI) InteractionRespond(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, resp)
	return nil
}

func (f *fakeAPI) InteractionResponseEdit(_ *discordgo.Interaction, e *discordgo.WebhookEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webEdits = append(f.webEdits, e)
	return &discordgo.Message{}, nil
}

// RequestWithBucketID handles the raw PATCH that clears a description.
func (f *fakeAPI) RequestWithBucketID(method, urlStr string, data interface{}, _ string, _ ...discordgo.RequestOption) ([]byte, error) {
	if method != "PATCH" {
		return nil, fmt.Errorf("unexpected %s %s", method, urlStr)
	}
	body, _ := data.(map[string]any)
	f.mu.Lock()
	f.rawPatches = append(f.rawPatches, body)
	var target *discordgo.GuildScheduledEvent
	for _, ev := range f.events {
		if discordgo.EndpointGuildScheduledEvent(ev.GuildID, ev.ID) == urlStr {
			target = ev
		}
	}
	f.mu.Unlock()
	if target == nil {
		return nil, errors.New("HTTP 404 Not Found")
	}
	raw, _ := json.Marshal(body)
	var p discordgo.GuildScheduledEventParams
	json.Unmarshal(raw, &p)
	ev, err := f.GuildScheduledEventEdit(target.GuildID, target.ID, &p)
	if err != nil {
		return nil, err
	}
	if v, ok := body["description"]; ok && v == nil {
		f.mu.Lock()
		f.events[ev.ID].Description = ""
		ev.Description = ""
		f.mu.Unlock()
	}
	return json.Marshal(ev)
}

func (f *fakeAPI) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeAPI) statusEdits() []editCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []editCall
	for _, e := range f.edits {
		if e.params.Status != 0 {
			out = append(out, e)
		}
	}
	return out
}

const (
	botID   = "999"
	guildID = "g1"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}
