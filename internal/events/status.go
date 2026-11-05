package events

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

// EventStatus is one upcoming or running event as the scheduler sees it, for
// the dashboard.
type EventStatus struct {
	ID         string
	GuildID    string
	Name       string
	Start      time.Time
	End        *time.Time
	Status     discordgo.GuildScheduledEventStatus
	EntityType discordgo.GuildScheduledEventEntityType
	ChannelID  string
	Location   string
	// Interested is the number of people who pressed Interested, as read at
	// startup and kept up to date from the gateway.
	Interested int
	// RemindAt is when the reminder goes out; zero when none is pending.
	RemindAt time.Time
	// Managed is true when the bot starts and ends the event.
	Managed bool
	// Failures counts failed status changes; RetryAt is the next attempt.
	Failures int
	RetryAt  time.Time
	GaveUp   bool
}

// SchedulerStatus is the scheduler's state, for the dashboard.
type SchedulerStatus struct {
	Running         bool
	ReminderChannel string
	RemindBefore    time.Duration
	AutoStart       bool
	// NextWake is when the scheduler acts next; zero when nothing is due.
	NextWake time.Time
	// Events are soonest first.
	Events []EventStatus
}

// Status returns the scheduler's configuration and every tracked event.
func (s *Scheduler) Status() SchedulerStatus {
	now := s.cfg.Now()
	st := SchedulerStatus{
		Running:         s.running.Load(),
		ReminderChannel: s.cfg.ChannelID,
		RemindBefore:    s.cfg.RemindBefore,
		AutoStart:       s.cfg.AutoStart,
		NextWake:        s.nextWake(now),
	}
	s.mu.Lock()
	evs := make([]*discordgo.GuildScheduledEvent, 0, len(s.events))
	byID := make(map[string]EventStatus, len(s.events))
	for id, t := range s.events {
		ev := t.ev
		es := EventStatus{
			ID: id, GuildID: ev.GuildID, Name: ev.Name,
			Start: ev.ScheduledStartTime, Status: ev.Status, EntityType: ev.EntityType,
			ChannelID: ev.ChannelID, Location: ev.EntityMetadata.Location,
			Interested: ev.UserCount,
			Managed:    s.manages(ev),
			Failures:   t.failures, RetryAt: t.retryAt, GaveUp: t.failures >= statusRetries,
		}
		if ev.ScheduledEndTime != nil {
			end := *ev.ScheduledEndTime
			es.End = &end
		}
		if t.remindPending {
			es.RemindAt = ev.ScheduledStartTime.Add(-s.cfg.RemindBefore)
		}
		byID[id] = es
		evs = append(evs, ev)
	}
	s.mu.Unlock()

	sortByStart(evs)
	for _, ev := range evs {
		st.Events = append(st.Events, byID[ev.ID])
	}
	return st
}

// AdjustInterested changes an event's interested count by delta, from the
// gateway's user add and remove events.
func (s *Scheduler) AdjustInterested(eventID string, delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.events[eventID]; ok {
		cp := *t.ev
		cp.UserCount = max(cp.UserCount+delta, 0)
		t.ev = &cp
	}
}
