package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/eventtime"
)

var t0 = time.Date(2026, 11, 6, 21, 0, 0, 0, eventtime.Bangkok) // event start

func ptr(t time.Time) *time.Time { return &t }

func externalEvent(id string) *discordgo.GuildScheduledEvent {
	return &discordgo.GuildScheduledEvent{
		ID: id, GuildID: guildID, CreatorID: botID, Name: "เตะบอล",
		ScheduledStartTime: t0, ScheduledEndTime: ptr(t0.Add(2 * time.Hour)),
		Status: discordgo.GuildScheduledEventStatusScheduled, EntityType: discordgo.GuildScheduledEventEntityTypeExternal,
		EntityMetadata: discordgo.GuildScheduledEventEntityMetadata{Location: "สนาม"},
	}
}

func voiceEvent(id string) *discordgo.GuildScheduledEvent {
	return &discordgo.GuildScheduledEvent{
		ID: id, GuildID: guildID, CreatorID: botID, Name: "เล่นเกม", ChannelID: "vc",
		ScheduledStartTime: t0, Status: discordgo.GuildScheduledEventStatusScheduled,
		EntityType: discordgo.GuildScheduledEventEntityTypeVoice,
	}
}

func newTestScheduler(api *fakeAPI, clock *fakeClock, mod func(*SchedulerConfig)) *Scheduler {
	cfg := SchedulerConfig{
		API: api, BotID: func() string { return botID }, ChannelID: "remind-ch",
		RemindBefore: 15 * time.Minute, AutoStart: true, Now: clock.Now,
	}
	if mod != nil {
		mod(&cfg)
	}
	return NewScheduler(cfg)
}

func interested(api *fakeAPI, eventID string, ids ...string) {
	for _, id := range ids {
		api.users[eventID] = append(api.users[eventID], &discordgo.GuildScheduledEventUser{
			GuildScheduledEventID: eventID, User: &discordgo.User{ID: id, Bot: strings.HasPrefix(id, "bot")},
		})
	}
}

func TestReminder_SentOnceWithMentions(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	ev := api.add(externalEvent("e1"))
	interested(api, "e1", "11", "22", "bot33")
	s.Upsert(ev)

	if next := s.RunDue(context.Background()); !next.Equal(t0.Add(-15 * time.Minute)) {
		t.Fatalf("next wake %s, want 15 min before start", next)
	}
	if api.sentCount() != 0 {
		t.Fatal("reminded too early")
	}

	clock.Set(t0.Add(-15 * time.Minute))
	s.RunDue(context.Background())
	clock.Set(t0.Add(-10 * time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 1 {
		t.Fatalf("sent %d reminders, want 1", api.sentCount())
	}
	m := api.sent[0]
	if m.channelID != "remind-ch" {
		t.Fatalf("channel %q", m.channelID)
	}
	for _, want := range []string{"<@11>", "<@22>", "เตะบอล", fmt.Sprintf("<t:%d:R>", t0.Unix()), "https://discord.com/events/g1/e1"} {
		if !strings.Contains(m.msg.Content, want) {
			t.Errorf("reminder %q lacks %q", m.msg.Content, want)
		}
	}
	if strings.Contains(m.msg.Content, "bot33") {
		t.Error("bots are mentioned")
	}
	if got := strings.Join(m.msg.AllowedMentions.Users, ","); got != "11,22" {
		t.Errorf("allowed mentions %q", got)
	}

	// A gateway update with the same start does not re-arm it.
	s.Upsert(ev)
	s.RunDue(context.Background())
	if api.sentCount() != 1 {
		t.Fatalf("sent %d reminders after an update, want 1", api.sentCount())
	}
}

func TestReminder_RestartSkipsPassedReminder(t *testing.T) {
	api := newFakeAPI()
	api.add(externalEvent("e1"))
	interested(api, "e1", "11")

	// The bot comes back 10 minutes before the start: the reminder time has
	// passed (and may have been sent by the previous run), so it is skipped.
	clock := &fakeClock{t: t0.Add(-10 * time.Minute)}
	s := newTestScheduler(api, clock, nil)
	if err := s.LoadGuild(guildID); err != nil {
		t.Fatal(err)
	}
	s.RunDue(context.Background())
	clock.Set(t0.Add(-time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 0 {
		t.Fatalf("sent %d reminders after restart, want 0", api.sentCount())
	}
}

func TestReminder_RestartBeforeReminderTimeStillReminds(t *testing.T) {
	api := newFakeAPI()
	api.add(externalEvent("e1"))
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	if err := s.LoadGuild(guildID); err != nil {
		t.Fatal(err)
	}
	clock.Set(t0.Add(-14 * time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 1 {
		t.Fatalf("sent %d reminders, want 1", api.sentCount())
	}
}

func TestReminder_RearmedWhenStartMoves(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-20 * time.Minute)}
	s := newTestScheduler(api, clock, nil)
	ev := externalEvent("e1")
	s.Upsert(ev)
	clock.Set(t0.Add(-15 * time.Minute))
	s.RunDue(context.Background())

	moved := *ev
	moved.ScheduledStartTime = t0.Add(24 * time.Hour)
	moved.ScheduledEndTime = ptr(t0.Add(26 * time.Hour))
	s.Upsert(&moved)
	clock.Set(moved.ScheduledStartTime.Add(-15 * time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 2 {
		t.Fatalf("sent %d reminders, want one per start time", api.sentCount())
	}
}

func TestReminder_Off(t *testing.T) {
	for name, mod := range map[string]func(*SchedulerConfig){
		"zero duration": func(c *SchedulerConfig) { c.RemindBefore = 0 },
		"no channel":    func(c *SchedulerConfig) { c.ChannelID = "" },
	} {
		api := newFakeAPI()
		clock := &fakeClock{t: t0.Add(-time.Hour)}
		s := newTestScheduler(api, clock, mod)
		s.Upsert(externalEvent("e1"))
		clock.Set(t0.Add(-5 * time.Minute))
		s.RunDue(context.Background())
		if api.sentCount() != 0 {
			t.Errorf("%s: sent a reminder", name)
		}
	}
}

func TestReminder_NotForActiveOrStartingEvent(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	ev := externalEvent("e1")
	s.Upsert(ev)
	// Someone starts it early by hand.
	active := *ev
	active.Status = discordgo.GuildScheduledEventStatusActive
	s.Upsert(&active)
	clock.Set(t0.Add(-15 * time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 0 {
		t.Fatal("reminded an event that already started")
	}
}

func TestReminder_PagesUsersAndCapsLength(t *testing.T) {
	api := newFakeAPI()
	var ids []string
	for i := 0; i < 150; i++ {
		ids = append(ids, fmt.Sprint(100000000000000000+i))
	}
	interested(api, "e1", ids...)
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(api.add(externalEvent("e1")))
	clock.Set(t0.Add(-15 * time.Minute))
	s.RunDue(context.Background())
	if api.sentCount() != 1 {
		t.Fatal("no reminder")
	}
	m := api.sent[0].msg
	if len(m.Content) > 2000 {
		t.Fatalf("reminder is %d characters", len(m.Content))
	}
	n := len(m.AllowedMentions.Users)
	if n == 0 || n >= 150 {
		t.Fatalf("mentioned %d users", n)
	}
	if !strings.Contains(m.Content, fmt.Sprintf("และอีก %d คน", 150-n)) {
		t.Fatalf("no count of the rest in %q", m.Content[len(m.Content)-200:])
	}
	if !strings.HasSuffix(m.Content, "https://discord.com/events/g1/e1") {
		t.Fatal("link is not last")
	}
}

func TestReminder_SendFailureIsNotRetried(t *testing.T) {
	api := newFakeAPI()
	api.sendErr = errors.New("missing access")
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(externalEvent("e1"))
	clock.Set(t0.Add(-15 * time.Minute))
	s.RunDue(context.Background())
	api.sendErr = nil
	s.RunDue(context.Background())
	if api.sentCount() != 0 {
		t.Fatal("a failed reminder was sent again")
	}
}

func TestAutoStartAndEnd_External(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Minute)}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(api.add(externalEvent("e1")))

	if next := s.RunDue(context.Background()); !next.Equal(t0) {
		t.Fatalf("next wake %s, want the start", next)
	}
	clock.Set(t0)
	next := s.RunDue(context.Background())
	if got := api.get("e1").Status; got != discordgo.GuildScheduledEventStatusActive {
		t.Fatalf("status at start = %d", got)
	}
	if !next.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("next wake %s, want the end", next)
	}
	clock.Set(t0.Add(time.Hour))
	s.RunDue(context.Background())
	if n := len(api.statusEdits()); n != 1 {
		t.Fatalf("%d status edits before the end", n)
	}
	clock.Set(t0.Add(2 * time.Hour))
	s.RunDue(context.Background())
	if got := api.get("e1").Status; got != discordgo.GuildScheduledEventStatusCompleted {
		t.Fatalf("status at end = %d", got)
	}
	if len(s.Upcoming(guildID)) != 0 {
		t.Fatal("a completed event is still tracked")
	}
}

func TestAutoStart_VoiceIsStartedButNotEnded(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(api.add(voiceEvent("v1")))
	s.RunDue(context.Background())
	if api.get("v1").Status != discordgo.GuildScheduledEventStatusActive {
		t.Fatal("voice event not started")
	}
	clock.Set(t0.Add(10 * time.Hour))
	if next := s.RunDue(context.Background()); !next.IsZero() {
		t.Fatalf("next wake %s, want none", next)
	}
	if n := len(api.statusEdits()); n != 1 {
		t.Fatalf("%d status edits; Discord ends voice events itself", n)
	}
}

func TestAutoStart_OthersOnlyWithAutoStart(t *testing.T) {
	human := func(id string) *discordgo.GuildScheduledEvent {
		ev := externalEvent(id)
		ev.CreatorID = "555"
		return ev
	}
	api := newFakeAPI()
	clock := &fakeClock{t: t0}
	s := newTestScheduler(api, clock, func(c *SchedulerConfig) { c.AutoStart = false })
	s.Upsert(api.add(human("h1")))
	s.Upsert(api.add(externalEvent("b1")))
	s.RunDue(context.Background())
	if api.get("h1").Status != discordgo.GuildScheduledEventStatusScheduled {
		t.Fatal("started someone else's event with EVENT_AUTO_START=false")
	}
	if api.get("b1").Status != discordgo.GuildScheduledEventStatusActive {
		t.Fatal("did not start the bot's own event")
	}

	api = newFakeAPI()
	s = newTestScheduler(api, clock, nil)
	s.Upsert(api.add(human("h1")))
	s.RunDue(context.Background())
	if api.get("h1").Status != discordgo.GuildScheduledEventStatusActive {
		t.Fatal("did not start someone else's event with EVENT_AUTO_START=true")
	}
}

func TestAutoStart_NeverStage(t *testing.T) {
	api := newFakeAPI()
	ev := voiceEvent("s1")
	ev.EntityType = discordgo.GuildScheduledEventEntityTypeStageInstance
	clock := &fakeClock{t: t0}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(api.add(ev))
	s.RunDue(context.Background())
	if len(api.statusEdits()) != 0 {
		t.Fatal("touched a stage event")
	}
}

func TestAutoStart_AfterRestart(t *testing.T) {
	api := newFakeAPI()
	api.add(externalEvent("late")) // start passed, end ahead: start now
	gone := externalEvent("gone")  // start and end passed while down: leave it
	gone.ScheduledStartTime = t0.Add(-5 * time.Hour)
	gone.ScheduledEndTime = ptr(t0.Add(-3 * time.Hour))
	api.add(gone)
	ended := externalEvent("ended") // was running, end passed while down: end it
	ended.Status = discordgo.GuildScheduledEventStatusActive
	ended.ScheduledStartTime = t0.Add(-3 * time.Hour)
	ended.ScheduledEndTime = ptr(t0.Add(-time.Hour))
	api.add(ended)
	oldVoice := voiceEvent("oldvoice") // voice without end, started 3 h ago: leave it
	oldVoice.ScheduledStartTime = t0.Add(-3 * time.Hour)
	api.add(oldVoice)

	clock := &fakeClock{t: t0.Add(30 * time.Minute)}
	s := newTestScheduler(api, clock, nil)
	if err := s.LoadGuild(guildID); err != nil {
		t.Fatal(err)
	}
	s.RunDue(context.Background())
	want := map[string]discordgo.GuildScheduledEventStatus{
		"late":     discordgo.GuildScheduledEventStatusActive,
		"gone":     discordgo.GuildScheduledEventStatusScheduled,
		"ended":    discordgo.GuildScheduledEventStatusCompleted,
		"oldvoice": discordgo.GuildScheduledEventStatusScheduled,
	}
	for id, st := range want {
		if got := api.get(id).Status; got != st {
			t.Errorf("%s: status %d, want %d", id, got, st)
		}
	}
	if api.sentCount() != 0 {
		t.Error("reminded after restart")
	}
}

func TestAutoStart_RetriesThenGivesUp(t *testing.T) {
	api := newFakeAPI()
	api.editErr = errors.New("HTTP 500")
	clock := &fakeClock{t: t0}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(api.add(externalEvent("e1")))

	next := s.RunDue(context.Background())
	if !next.Equal(t0.Add(time.Minute)) {
		t.Fatalf("retry at %s, want a minute later", next)
	}
	s.RunDue(context.Background()) // not yet
	if n := len(api.statusEdits()); n != 1 {
		t.Fatalf("%d attempts before the retry time", n)
	}
	for i := 0; i < 5; i++ {
		clock.Set(clock.Now().Add(10 * time.Minute))
		s.RunDue(context.Background())
	}
	if n := len(api.statusEdits()); n != statusRetries {
		t.Fatalf("%d attempts, want %d", n, statusRetries)
	}
}

func TestScheduler_RemoveAndCancel(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0.Add(-time.Hour)}
	s := newTestScheduler(api, clock, nil)
	s.Upsert(externalEvent("a"))
	s.Upsert(externalEvent("b"))
	canceled := externalEvent("b")
	canceled.Status = discordgo.GuildScheduledEventStatusCanceled
	s.Upsert(canceled)
	s.Remove("a")
	clock.Set(t0)
	s.RunDue(context.Background())
	if len(api.statusEdits()) != 0 || api.sentCount() != 0 || len(s.Upcoming(guildID)) != 0 {
		t.Fatal("acted on a removed or canceled event")
	}
}

func TestScheduler_SyncDropsVanishedEvents(t *testing.T) {
	api := newFakeAPI()
	s := newTestScheduler(api, &fakeClock{t: t0.Add(-time.Hour)}, nil)
	s.Upsert(externalEvent("a"))
	other := externalEvent("x")
	other.GuildID = "g2"
	s.Upsert(other)
	s.Sync(guildID, []*discordgo.GuildScheduledEvent{externalEvent("b")})
	got := s.Upcoming(guildID)
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("upcoming %v", got)
	}
	if len(s.Upcoming("g2")) != 1 {
		t.Fatal("sync of one guild dropped another guild's events")
	}
}

func TestScheduler_UpcomingSorted(t *testing.T) {
	s := newTestScheduler(newFakeAPI(), &fakeClock{t: t0.Add(-time.Hour)}, nil)
	late := externalEvent("late")
	late.ScheduledStartTime = t0.Add(time.Hour)
	s.Upsert(late)
	s.Upsert(externalEvent("soon"))
	got := s.Upcoming(guildID)
	if len(got) != 2 || got[0].ID != "soon" || got[1].ID != "late" {
		t.Fatalf("order %s, %s", got[0].ID, got[1].ID)
	}
}

func TestScheduler_Creator(t *testing.T) {
	s := newTestScheduler(newFakeAPI(), &fakeClock{t: t0}, nil)
	mine := externalEvent("mine")
	if got := s.Creator(mine); got != "" {
		t.Fatalf("bot event without a recorded creator: %q", got)
	}
	s.SetCreator("mine", "42")
	if got := s.Creator(mine); got != "42" {
		t.Fatalf("recorded creator: %q", got)
	}
	human := externalEvent("h")
	human.CreatorID = "77"
	if got := s.Creator(human); got != "77" {
		t.Fatalf("Discord creator: %q", got)
	}
}

func TestScheduler_RunLoop(t *testing.T) {
	api := newFakeAPI()
	clock := &fakeClock{t: t0}
	s := newTestScheduler(api, clock, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	// An event arriving from the gateway wakes the loop.
	s.Upsert(api.add(externalEvent("e1")))
	deadline := time.Now().Add(5 * time.Second)
	for api.get("e1").Status != discordgo.GuildScheduledEventStatusActive {
		if time.Now().After(deadline) {
			t.Fatal("the loop did not start the event")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
