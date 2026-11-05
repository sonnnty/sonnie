package events

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestStatus_ListsEventsWithReminderAndManagement(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, func(c *SchedulerConfig) { c.AutoStart = false })

	mine := externalEvent("e1")
	mine.UserCount = 4
	theirs := voiceEvent("e2")
	theirs.CreatorID = "someone"
	theirs.ScheduledStartTime = t0.Add(time.Hour)
	s.Upsert(mine)
	s.Upsert(theirs)

	st := s.Status()
	if st.Running || st.ReminderChannel != "remind-ch" || st.RemindBefore != 15*time.Minute || st.AutoStart {
		t.Fatalf("config %+v", st)
	}
	if want := t0.Add(-15 * time.Minute); !st.NextWake.Equal(want) {
		t.Fatalf("next wake %s, want %s", st.NextWake, want)
	}
	if len(st.Events) != 2 || st.Events[0].ID != "e1" || st.Events[1].ID != "e2" {
		t.Fatalf("events %+v", st.Events)
	}
	e1, e2 := st.Events[0], st.Events[1]
	if !e1.Managed || e2.Managed {
		t.Fatalf("managed: e1 %v e2 %v", e1.Managed, e2.Managed)
	}
	if !e1.RemindAt.Equal(t0.Add(-15*time.Minute)) || e1.Location != "สนาม" || e1.End == nil || e1.Interested != 4 {
		t.Fatalf("e1 %+v", e1)
	}
	if e2.ChannelID != "vc" || e2.Status != discordgo.GuildScheduledEventStatusScheduled {
		t.Fatalf("e2 %+v", e2)
	}
}

func TestStatus_InterestedCountSurvivesGatewayUpdates(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)

	ev := externalEvent("e1")
	ev.UserCount = 2
	s.Upsert(ev)
	s.AdjustInterested("e1", 1)
	s.AdjustInterested("missing", 1)

	update := externalEvent("e1") // gateway updates carry no user_count
	update.Name = "renamed"
	s.Upsert(update)
	if got := s.Status().Events[0]; got.Interested != 3 || got.Name != "renamed" {
		t.Fatalf("after update: %+v", got)
	}
	s.AdjustInterested("e1", -5)
	if got := s.Status().Events[0].Interested; got != 0 {
		t.Fatalf("count went to %d", got)
	}
}
