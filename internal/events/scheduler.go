package events

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/eventtime"
)

const (
	// statusRetries is how often a failed start or end is retried, a minute
	// apart times the attempt, before the bot gives up on that event.
	statusRetries = 3
	// maxWait re-checks the events at least this often.
	maxWait = time.Hour
	// usersPageSize is Discord's page size for the interested users.
	usersPageSize = 100
	// maxUserPages bounds how many interested users are fetched.
	maxUserPages = 10
	// reminderMaxLen leaves room under Discord's 2000-character limit.
	reminderMaxLen = 1900
)

// SchedulerConfig configures the reminders and the automatic start and end.
type SchedulerConfig struct {
	API API
	// BotID is the bot's user ID, known once the session is open.
	BotID func() string
	// ChannelID receives reminders; empty turns them off.
	ChannelID string
	// RemindBefore is how long before the start to remind; 0 turns it off.
	RemindBefore time.Duration
	// AutoStart also starts and ends events the bot did not create.
	AutoStart bool
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Scheduler keeps the guilds' upcoming events in memory, reminds the
// interested users before an event starts, and starts and ends events.
//
// It keeps no state across restarts: after a restart the events are read
// again (Sync), reminders whose time has already come are skipped rather
// than sent late or twice, and events that should be running are started.
type Scheduler struct {
	cfg  SchedulerConfig
	kick chan struct{}

	mu     sync.Mutex
	events map[string]*tracked // by event ID
	// creators remembers who ran /event create, since Discord records the
	// bot as the creator. It is lost on restart.
	creators map[string]string

	running atomic.Bool // Run is looping
}

type tracked struct {
	ev *discordgo.GuildScheduledEvent
	// remindPending is set when the event is first seen, or its start time
	// changes, while the reminder time is still ahead.
	remindPending bool
	// After a failed status change.
	failures int
	retryAt  time.Time
}

func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.BotID == nil {
		cfg.BotID = func() string { return "" }
	}
	if cfg.ChannelID == "" {
		cfg.RemindBefore = 0
	}
	return &Scheduler{
		cfg:      cfg,
		kick:     make(chan struct{}, 1),
		events:   map[string]*tracked{},
		creators: map[string]string{},
	}
}

func (s *Scheduler) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Upsert records a created or updated event (from the gateway, a REST
// reply or the startup list).
func (s *Scheduler) Upsert(ev *discordgo.GuildScheduledEvent) {
	if ev == nil || ev.ID == "" {
		return
	}
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !upcoming(ev) {
		delete(s.events, ev.ID)
		delete(s.creators, ev.ID)
		s.wake()
		return
	}
	cp := *ev
	t, ok := s.events[ev.ID]
	if ok && cp.UserCount == 0 {
		// Only the startup list asks Discord for the interested count;
		// gateway updates and edit replies leave it out. Keep the count
		// (adjusted by AdjustInterested) rather than drop it to zero.
		cp.UserCount = t.ev.UserCount
	}
	if !ok {
		t = &tracked{}
		s.events[ev.ID] = t
	}
	if !ok || !t.ev.ScheduledStartTime.Equal(ev.ScheduledStartTime) {
		t.remindPending = s.cfg.RemindBefore > 0 && ev.ScheduledStartTime.Add(-s.cfg.RemindBefore).After(now)
	}
	if ok && (t.ev.Status != ev.Status || !t.ev.ScheduledStartTime.Equal(ev.ScheduledStartTime) || !sameEnd(t.ev, ev)) {
		t.failures, t.retryAt = 0, time.Time{}
	}
	t.ev = &cp
	s.wake()
}

func sameEnd(a, b *discordgo.GuildScheduledEvent) bool {
	if a.ScheduledEndTime == nil || b.ScheduledEndTime == nil {
		return a.ScheduledEndTime == nil && b.ScheduledEndTime == nil
	}
	return a.ScheduledEndTime.Equal(*b.ScheduledEndTime)
}

// Remove forgets a deleted event.
func (s *Scheduler) Remove(eventID string) {
	s.mu.Lock()
	delete(s.events, eventID)
	delete(s.creators, eventID)
	s.mu.Unlock()
	s.wake()
}

// Sync replaces what is known about a guild's events with list.
func (s *Scheduler) Sync(guildID string, list []*discordgo.GuildScheduledEvent) {
	seen := map[string]bool{}
	for _, ev := range list {
		seen[ev.ID] = true
		s.Upsert(ev)
	}
	s.mu.Lock()
	for id, t := range s.events {
		if t.ev.GuildID == guildID && !seen[id] {
			delete(s.events, id)
			delete(s.creators, id)
		}
	}
	s.mu.Unlock()
	s.wake()
}

// LoadGuild reads a guild's events from Discord and syncs them.
func (s *Scheduler) LoadGuild(guildID string) error {
	list, err := s.cfg.API.GuildScheduledEvents(guildID, true)
	if err != nil {
		return fmt.Errorf("events: list guild %s: %w", guildID, err)
	}
	s.Sync(guildID, list)
	return nil
}

// Upcoming returns a guild's scheduled and active events, soonest first.
func (s *Scheduler) Upcoming(guildID string) []*discordgo.GuildScheduledEvent {
	s.mu.Lock()
	var out []*discordgo.GuildScheduledEvent
	for _, t := range s.events {
		if t.ev.GuildID == guildID {
			cp := *t.ev
			out = append(out, &cp)
		}
	}
	s.mu.Unlock()
	sortByStart(out)
	return out
}

// SetCreator records who created an event through the bot.
func (s *Scheduler) SetCreator(eventID, userID string) {
	s.mu.Lock()
	s.creators[eventID] = userID
	s.mu.Unlock()
}

// Creator is the user who created the event: the one who ran /event create
// for the bot's own events, else Discord's creator.
func (s *Scheduler) Creator(ev *discordgo.GuildScheduledEvent) string {
	s.mu.Lock()
	id, ok := s.creators[ev.ID]
	s.mu.Unlock()
	if ok {
		return id
	}
	if ev.CreatorID == s.cfg.BotID() {
		return ""
	}
	return ev.CreatorID
}

// Run performs due actions until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)
	for {
		next := s.RunDue(ctx)
		wait := maxWait
		if !next.IsZero() {
			wait = min(max(next.Sub(s.cfg.Now()), 0), maxWait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.kick:
		case <-timer.C:
		}
		timer.Stop()
	}
}

type actionKind int

const (
	actRemind actionKind = iota
	actStart
	actComplete
)

type action struct {
	kind actionKind
	ev   *discordgo.GuildScheduledEvent
}

// manages reports whether the bot starts and ends ev: its own events
// always, others with AutoStart. Stage events need a stage instance and
// are left to people.
func (s *Scheduler) manages(ev *discordgo.GuildScheduledEvent) bool {
	if ev.EntityType != discordgo.GuildScheduledEventEntityTypeVoice && ev.EntityType != discordgo.GuildScheduledEventEntityTypeExternal {
		return false
	}
	return s.cfg.AutoStart || (ev.CreatorID != "" && ev.CreatorID == s.cfg.BotID())
}

// dueStart: started at or after its start time, unless it should already
// have ended (bot down the whole time). A voice event without an end is
// still started within the default duration.
func dueStart(ev *discordgo.GuildScheduledEvent, now time.Time) bool {
	if ev.Status != discordgo.GuildScheduledEventStatusScheduled || now.Before(ev.ScheduledStartTime) {
		return false
	}
	if ev.ScheduledEndTime != nil {
		return now.Before(*ev.ScheduledEndTime)
	}
	return now.Before(ev.ScheduledStartTime.Add(eventtime.DefaultDuration))
}

// dueComplete: external events are ended at their end time; Discord ends
// voice events itself when the channel empties.
func dueComplete(ev *discordgo.GuildScheduledEvent, now time.Time) bool {
	return ev.Status == discordgo.GuildScheduledEventStatusActive &&
		ev.EntityType == discordgo.GuildScheduledEventEntityTypeExternal &&
		ev.ScheduledEndTime != nil && !now.Before(*ev.ScheduledEndTime)
}

// RunDue performs what is due now and returns when to look again (zero
// when nothing is pending).
func (s *Scheduler) RunDue(ctx context.Context) time.Time {
	now := s.cfg.Now()
	var actions []action
	s.mu.Lock()
	for _, t := range s.events {
		ev := t.ev
		if t.remindPending && !now.Before(ev.ScheduledStartTime.Add(-s.cfg.RemindBefore)) {
			t.remindPending = false
			if now.Before(ev.ScheduledStartTime) && ev.Status == discordgo.GuildScheduledEventStatusScheduled {
				actions = append(actions, action{actRemind, ev})
			}
		}
		if !s.manages(ev) || t.failures >= statusRetries || now.Before(t.retryAt) {
			continue
		}
		switch {
		case dueStart(ev, now):
			actions = append(actions, action{actStart, ev})
		case dueComplete(ev, now):
			actions = append(actions, action{actComplete, ev})
		}
	}
	s.mu.Unlock()

	sort.Slice(actions, func(i, j int) bool { return actions[i].ev.ScheduledStartTime.Before(actions[j].ev.ScheduledStartTime) })
	for _, a := range actions {
		switch a.kind {
		case actRemind:
			s.remind(ctx, a.ev)
		case actStart:
			s.setStatus(a.ev, discordgo.GuildScheduledEventStatusActive, now)
		case actComplete:
			s.setStatus(a.ev, discordgo.GuildScheduledEventStatusCompleted, now)
		}
	}
	return s.nextWake(now)
}

func (s *Scheduler) nextWake(now time.Time) time.Time {
	var next time.Time
	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.events {
		ev := t.ev
		if t.remindPending {
			consider(ev.ScheduledStartTime.Add(-s.cfg.RemindBefore))
		}
		if !s.manages(ev) || t.failures >= statusRetries {
			continue
		}
		consider(t.retryAt)
		switch ev.Status {
		case discordgo.GuildScheduledEventStatusScheduled:
			consider(ev.ScheduledStartTime)
		case discordgo.GuildScheduledEventStatusActive:
			if ev.EntityType == discordgo.GuildScheduledEventEntityTypeExternal && ev.ScheduledEndTime != nil {
				consider(*ev.ScheduledEndTime)
			}
		}
	}
	return next
}

func (s *Scheduler) setStatus(ev *discordgo.GuildScheduledEvent, status discordgo.GuildScheduledEventStatus, now time.Time) {
	updated, err := s.cfg.API.GuildScheduledEventEdit(ev.GuildID, ev.ID, &discordgo.GuildScheduledEventParams{Status: status})
	if err != nil {
		s.mu.Lock()
		if t, ok := s.events[ev.ID]; ok {
			t.failures++
			t.retryAt = now.Add(time.Duration(t.failures) * time.Minute)
			if t.failures >= statusRetries {
				log.Printf("events: set event %s (%q) to status %d: %v; giving up", ev.ID, ev.Name, status, err)
			} else {
				log.Printf("events: set event %s (%q) to status %d: %v; retrying", ev.ID, ev.Name, status, err)
			}
		}
		s.mu.Unlock()
		return
	}
	log.Printf("events: event %s (%q) set to status %d", ev.ID, ev.Name, status)
	if updated == nil {
		cp := *ev
		cp.Status = status
		updated = &cp
	}
	s.Upsert(updated)
}

func (s *Scheduler) remind(ctx context.Context, ev *discordgo.GuildScheduledEvent) {
	users, err := s.interested(ev)
	if err != nil {
		// Still remind, without mentions.
		log.Printf("events: interested users of %s: %v", ev.ID, err)
	}
	msg := ReminderMessage(ev, users)
	if _, err := s.cfg.API.ChannelMessageSendComplex(s.cfg.ChannelID, msg); err != nil {
		log.Printf("events: reminder for %s in channel %s: %v", ev.ID, s.cfg.ChannelID, err)
		return
	}
	log.Printf("events: reminded %d users of %s (%q)", len(users), ev.ID, ev.Name)
}

// interested returns the IDs of the people (not bots) who pressed
// Interested.
func (s *Scheduler) interested(ev *discordgo.GuildScheduledEvent) ([]string, error) {
	var ids []string
	after := ""
	for page := 0; page < maxUserPages; page++ {
		users, err := s.cfg.API.GuildScheduledEventUsers(ev.GuildID, ev.ID, usersPageSize, false, "", after)
		if err != nil {
			return ids, err
		}
		for _, u := range users {
			if u.User == nil {
				continue
			}
			if !u.User.Bot {
				ids = append(ids, u.User.ID)
			}
			// With after set, Discord returns users in ascending ID order.
			after = u.User.ID
		}
		if len(users) < usersPageSize {
			break
		}
	}
	return ids, nil
}

// ReminderMessage mentions the interested users and links the event, which
// Discord unfurls into a card with the cover.
func ReminderMessage(ev *discordgo.GuildScheduledEvent, userIDs []string) *discordgo.MessageSend {
	start := ev.ScheduledStartTime.Unix()
	head := fmt.Sprintf("⏰ **%s** เริ่ม <t:%d:R> (<t:%d:t>)", ev.Name, start, start)
	link := Link(ev.GuildID, ev.ID)
	var b strings.Builder
	b.WriteString(head)
	var mentioned []string
	if len(userIDs) > 0 {
		b.WriteString("\n")
		for i, id := range userIDs {
			m := "<@" + id + "> "
			if b.Len()+len(m)+len(link)+40 > reminderMaxLen {
				fmt.Fprintf(&b, "และอีก %d คน", len(userIDs)-i)
				break
			}
			b.WriteString(m)
			mentioned = append(mentioned, id)
		}
	}
	b.WriteString("\n")
	b.WriteString(link)
	return &discordgo.MessageSend{
		Content: strings.TrimRight(b.String(), " "),
		// Ping only the listed users, never @everyone or roles from a name.
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}, Users: mentioned},
	}
}
