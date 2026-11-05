package panels

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/events"
)

// EventSource is the events scheduler.
type EventSource interface {
	Status() events.SchedulerStatus
}

// CoverSource is the cover provider chain.
type CoverSource interface {
	Status() []cover.ProviderStatus
}

// EventsData is the Events panel's JSON.
type EventsData struct {
	Scheduler SchedulerData `json:"scheduler"`
	Events    []EventData   `json:"events"`
	Covers    []CoverData   `json:"covers"`
}

// SchedulerData is the reminder and auto start/end loop.
type SchedulerData struct {
	Running             bool       `json:"running"`
	ReminderChannel     *Ref       `json:"reminder_channel,omitempty"`
	RemindBeforeMinutes int        `json:"remind_before_minutes"`
	AutoStart           bool       `json:"auto_start"`
	NextWake            *time.Time `json:"next_wake,omitempty"`
}

// EventData is one upcoming or running event.
type EventData struct {
	ID         string     `json:"id"`
	Guild      Ref        `json:"guild"`
	Name       string     `json:"name"`
	Start      time.Time  `json:"start"`
	End        *time.Time `json:"end,omitempty"`
	Status     string     `json:"status"`
	Kind       string     `json:"kind"`
	Where      string     `json:"where,omitempty"`
	Interested int        `json:"interested"`
	RemindAt   *time.Time `json:"remind_at,omitempty"`
	Managed    bool       `json:"managed"`
	Failures   int        `json:"failures"`
	RetryAt    *time.Time `json:"retry_at,omitempty"`
	GaveUp     bool       `json:"gave_up"`
	URL        string     `json:"url"`
}

// CoverData is one cover provider in the order they are tried.
type CoverData struct {
	Name        string     `json:"name"`
	PausedUntil *time.Time `json:"paused_until,omitempty"`
}

// Events shows the upcoming events, pending reminders, the scheduler and the
// cover providers with Brave's pause.
func Events(sched EventSource, covers CoverSource, names Names) dashboard.Panel {
	names = orNoNames(names)
	return dashboard.Panel{ID: "events", Title: "อีเวนต์", Snapshot: func(context.Context) (any, error) {
		st := sched.Status()
		d := EventsData{
			Scheduler: SchedulerData{
				Running:         st.Running,
				ReminderChannel: channelRef(names, st.ReminderChannel),
				AutoStart:       st.AutoStart,
				NextWake:        timePtr(st.NextWake),
			},
			Events: []EventData{},
			Covers: []CoverData{},
		}
		if st.ReminderChannel != "" {
			d.Scheduler.RemindBeforeMinutes = int(st.RemindBefore / time.Minute)
		}
		for _, e := range st.Events {
			ed := EventData{
				ID: e.ID, Guild: guildRef(names, e.GuildID), Name: e.Name,
				Start: e.Start.UTC(), Status: eventStatus(e.Status), Kind: eventKind(e.EntityType),
				Interested: e.Interested, RemindAt: timePtr(e.RemindAt), Managed: e.Managed,
				Failures: e.Failures, RetryAt: timePtr(e.RetryAt), GaveUp: e.GaveUp,
				URL: events.Link(e.GuildID, e.ID),
			}
			if e.End != nil {
				ed.End = timePtr(*e.End)
			}
			switch {
			case e.ChannelID != "":
				if n := names.Channel(e.ChannelID); n != "" {
					ed.Where = "🔊 " + n
				} else {
					ed.Where = "🔊 " + e.ChannelID
				}
			case e.Location != "":
				ed.Where = "📍 " + e.Location
			}
			d.Events = append(d.Events, ed)
		}
		if covers != nil {
			for _, c := range covers.Status() {
				d.Covers = append(d.Covers, CoverData{Name: c.Name, PausedUntil: timePtr(c.PausedUntil)})
			}
		}
		return d, nil
	}}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func eventStatus(s discordgo.GuildScheduledEventStatus) string {
	switch s {
	case discordgo.GuildScheduledEventStatusScheduled:
		return "scheduled"
	case discordgo.GuildScheduledEventStatusActive:
		return "active"
	case discordgo.GuildScheduledEventStatusCompleted:
		return "completed"
	case discordgo.GuildScheduledEventStatusCanceled:
		return "canceled"
	}
	return "unknown"
}

func eventKind(t discordgo.GuildScheduledEventEntityType) string {
	switch t {
	case discordgo.GuildScheduledEventEntityTypeVoice:
		return "voice"
	case discordgo.GuildScheduledEventEntityTypeStageInstance:
		return "stage"
	case discordgo.GuildScheduledEventEntityTypeExternal:
		return "external"
	}
	return "unknown"
}
