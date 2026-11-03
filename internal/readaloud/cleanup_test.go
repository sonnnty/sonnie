package readaloud

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

var (
	cleanNow   = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cleanStart = cleanNow.Add(-time.Hour)
	cleanEnd   = cleanNow
)

// cleanupFixture is a channel with a session message at cleanStart.
type cleanupFixture struct {
	f       *fakeMessenger
	job     cleanupJob
	session string
}

func newCleanupFixture(participants ...string) *cleanupFixture {
	f := newFakeMessenger()
	f.channels["t1"] = nil
	session := f.add("t1", "bot", cleanStart)
	set := make(map[string]time.Time)
	for _, p := range participants {
		set[p] = time.Time{} // from the session start
	}
	return &cleanupFixture{f: f, session: session, job: cleanupJob{
		ChannelID:        "t1",
		SessionMessageID: session,
		Started:          cleanStart,
		Ended:            cleanEnd,
		Participants:     set,
	}}
}

func (c *cleanupFixture) run() CleanupResult {
	return cleanupMessages(context.Background(), c.f, c.job, cleanNow)
}

func TestCleanup_DeletesOnlyParticipantMessagesInWindow(t *testing.T) {
	c := newCleanupFixture("a", "b")
	in := cleanStart.Add(10 * time.Minute)

	// One message positioned before the session message, one listed after it
	// but timestamped earlier (clock skew): neither may be deleted.
	c.f.channels["t1"] = append([]*fakeMsg{{id: "pre", author: "a", time: cleanStart.Add(-time.Minute)}}, c.f.channels["t1"]...)
	before := "pre"
	skewed := c.f.add("t1", "a", cleanStart.Add(-time.Second))

	wantGone := []string{
		c.f.add("t1", "a", in),
		c.f.add("t1", "b", in.Add(time.Minute)), // joined later, still a participant
	}
	stranger := c.f.add("t1", "stranger", in)
	other := c.f.add("t2", "a", in) // another channel
	after := c.f.add("t1", "a", cleanEnd.Add(time.Second))

	res := c.run()

	if res.Deleted != 2 || res.Failed != 0 {
		t.Fatalf("result %+v, want 2 deleted", res)
	}
	for _, id := range wantGone {
		if !c.f.isDeleted(id) {
			t.Errorf("participant message %s was not deleted", id)
		}
	}
	for name, id := range map[string]string{
		"session message": c.session, "non-participant": stranger,
		"other channel": other, "before the session": before, "before the start (time)": skewed, "after the end": after,
	} {
		if c.f.isDeleted(id) {
			t.Errorf("%s (%s) must not be deleted", name, id)
		}
	}
}

func TestCleanup_NeverDeletesSessionMessageEvenIfAuthorIsParticipant(t *testing.T) {
	// The bot's ID in the participant set must not make its own message a
	// candidate.
	c := newCleanupFixture("bot")
	c.f.add("t1", "bot", cleanStart.Add(time.Minute))
	res := c.run()
	if c.f.isDeleted(c.session) {
		t.Fatal("session message was deleted")
	}
	if res.Deleted != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestCleanup_BulkChunksOfAtMost100(t *testing.T) {
	c := newCleanupFixture("a")
	const n = 250
	for i := 0; i < n; i++ {
		c.f.add("t1", "a", cleanStart.Add(time.Duration(i+1)*time.Second))
	}
	res := c.run()

	if res.Deleted != n || res.Failed != 0 {
		t.Fatalf("result %+v", res)
	}
	var sizes []int
	for _, ids := range c.f.bulk {
		sizes = append(sizes, len(ids))
	}
	if want := []int{100, 100, 50}; !reflect.DeepEqual(sizes, want) {
		t.Fatalf("bulk chunk sizes %v, want %v", sizes, want)
	}
	if len(c.f.single) != 0 {
		t.Fatalf("unexpected single deletes: %v", c.f.single)
	}
	if got := c.f.alive("t1"); len(got) != 1 || got[0] != c.session {
		t.Fatalf("remaining messages %v, want only the session message", got)
	}
}

func TestCleanup_LeftoverOfOneChunkIsDeletedSingly(t *testing.T) {
	c := newCleanupFixture("a")
	for i := 0; i < 101; i++ { // 100 + 1: bulk delete needs at least two
		c.f.add("t1", "a", cleanStart.Add(time.Duration(i+1)*time.Second))
	}
	res := c.run()
	if res.Deleted != 101 {
		t.Fatalf("result %+v", res)
	}
	if len(c.f.bulk) != 1 || len(c.f.bulk[0]) != 100 || len(c.f.single) != 1 {
		t.Fatalf("bulk %d calls, single %v; want one bulk of 100 and one single", len(c.f.bulk), c.f.single)
	}
}

func TestCleanup_OldMessagesAreDeletedOneByOne(t *testing.T) {
	// A session can outlive Discord's 14-day bulk limit.
	c := newCleanupFixture("a")
	c.job.Started = cleanNow.Add(-20 * 24 * time.Hour)
	c.f.channels["t1"][0].time = c.job.Started

	old1 := c.f.add("t1", "a", cleanNow.Add(-15*24*time.Hour))
	old2 := c.f.add("t1", "a", cleanNow.Add(-14*24*time.Hour-time.Minute))
	young1 := c.f.add("t1", "a", cleanNow.Add(-2*24*time.Hour))
	young2 := c.f.add("t1", "a", cleanNow.Add(-time.Hour))

	res := c.run()

	if res.Deleted != 4 {
		t.Fatalf("result %+v", res)
	}
	sort.Strings(c.f.single)
	if want := []string{old1, old2}; !reflect.DeepEqual(c.f.single, want) {
		t.Fatalf("single deletes %v, want %v", c.f.single, want)
	}
	if len(c.f.bulk) != 1 || !reflect.DeepEqual(c.f.bulk[0], []string{young1, young2}) {
		t.Fatalf("bulk deletes %v, want one call with %v", c.f.bulk, []string{young1, young2})
	}
}

func TestCleanup_BulkFailureFallsBackToSingles(t *testing.T) {
	c := newCleanupFixture("a")
	c.f.bulkErr = errors.New("a message is too old")
	c.f.add("t1", "a", cleanStart.Add(time.Minute))
	c.f.add("t1", "a", cleanStart.Add(2*time.Minute))
	c.f.add("t1", "a", cleanStart.Add(3*time.Minute))

	res := c.run()
	if res.Deleted != 3 || res.Failed != 0 || len(c.f.single) != 3 {
		t.Fatalf("result %+v, singles %v", res, c.f.single)
	}
}

func TestCleanup_CountsFailuresAndToleratesGoneMessages(t *testing.T) {
	c := newCleanupFixture("a")
	c.job.Started = cleanNow.Add(-30 * 24 * time.Hour)
	c.f.channels["t1"][0].time = c.job.Started
	c.f.add("t1", "a", cleanNow.Add(-20*24*time.Hour))
	c.f.add("t1", "a", cleanNow.Add(-19*24*time.Hour))

	c.f.deleteErr = errors.New("forbidden")
	res := c.run()
	if res.Deleted != 0 || res.Failed != 2 {
		t.Fatalf("result %+v, want 2 failed", res)
	}

	c.f.deleteErr = ErrMessageGone
	res = c.run()
	if res.Deleted != 0 || res.Failed != 0 {
		t.Fatalf("already-deleted messages must count as neither: %+v", res)
	}
}

func TestCleanup_ScanFailureFallsBackToTrackedMessages(t *testing.T) {
	c := newCleanupFixture("a")
	c.f.scanErr = errors.New("missing Read Message History")
	mine := c.f.add("t1", "a", cleanStart.Add(time.Minute))
	mine2 := c.f.add("t1", "a", cleanStart.Add(2*time.Minute))
	stranger := c.f.add("t1", "stranger", cleanStart.Add(time.Minute))
	c.job.Tracked = map[string]time.Time{
		mine:  cleanStart.Add(time.Minute),
		mine2: cleanStart.Add(2 * time.Minute),
		// Tracked after the end must still respect the window.
		"late": cleanEnd.Add(time.Minute),
	}

	res := c.run()

	if res.Deleted != 2 {
		t.Fatalf("result %+v, want the 2 tracked messages", res)
	}
	if !c.f.isDeleted(mine) || !c.f.isDeleted(mine2) || c.f.isDeleted(stranger) || c.f.isDeleted(c.session) {
		t.Fatalf("wrong messages deleted: alive %v", c.f.alive("t1"))
	}
}

func TestCleanup_ScanIsAuthoritativeWhenItSucceeds(t *testing.T) {
	// A tracked message the scan no longer lists was deleted by its author;
	// it must not be deleted again or counted.
	c := newCleanupFixture("a")
	c.f.add("t1", "a", cleanStart.Add(time.Minute))
	c.job.Tracked = map[string]time.Time{"gone": cleanStart.Add(time.Minute)}

	res := c.run()
	if res.Deleted != 1 || res.Failed != 0 {
		t.Fatalf("result %+v, want exactly the one listed message", res)
	}
	if len(c.f.single) != 1 || c.f.single[0] == "gone" || len(c.f.bulk) != 0 {
		t.Fatalf("bulk %v single %v", c.f.bulk, c.f.single)
	}
}

func TestCleanup_ScanCatchesMessagesTheBotNeverSaw(t *testing.T) {
	// After a restart nothing is tracked; the scan finds everything.
	c := newCleanupFixture("a")
	for i := 0; i < 3; i++ {
		c.f.add("t1", "a", cleanStart.Add(time.Duration(i+1)*time.Minute))
	}
	res := c.run()
	if res.Deleted != 3 {
		t.Fatalf("result %+v", res)
	}
}

func TestCleanup_StopsWhenContextIsDone(t *testing.T) {
	c := newCleanupFixture("a")
	c.job.Started = cleanNow.Add(-30 * 24 * time.Hour)
	c.f.channels["t1"][0].time = c.job.Started
	for i := 0; i < 3; i++ {
		c.f.add("t1", "a", cleanNow.Add(-time.Duration(20+i)*24*time.Hour))
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.f.onDelete = func(string) { cancel() }

	res := cleanupMessages(ctx, c.f, c.job, cleanNow)
	if res.Deleted != 1 || res.Failed != 2 {
		t.Fatalf("result %+v, want 1 deleted and 2 failed", res)
	}
}

func TestSummaryText(t *testing.T) {
	people := []string{"1", "2"}
	tests := []struct {
		name string
		s    summary
		want string
	}{
		{"deleted", summary{people, 25 * time.Minute, CleanupResult{Deleted: 42}},
			"📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: <@1>, <@2> · 25 นาที · ลบข้อความแล้ว 42 ข้อความ"},
		{"failures", summary{people, time.Hour, CleanupResult{Deleted: 3, Failed: 2}},
			"📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: <@1>, <@2> · 1 ชั่วโมง · ลบข้อความแล้ว 3 ข้อความ (ลบไม่ได้ 2)"},
		{"no permission", summary{people[:1], 90 * time.Minute, CleanupResult{NoPermission: true}},
			"📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: <@1> · 1 ชั่วโมง 30 นาที · ไม่ได้ลบข้อความ (บอทไม่มีสิทธิ์ Manage Messages)"},
		{"short", summary{nil, 20 * time.Second, CleanupResult{}},
			"📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: - · ไม่ถึง 1 นาที · ลบข้อความแล้ว 0 ข้อความ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summaryText(tt.s); got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestMentions_CapsLongLists(t *testing.T) {
	var ids []string
	for i := 0; i < maxListedMentions+3; i++ {
		ids = append(ids, fmt.Sprint(i))
	}
	got := mentions(ids, " ")
	if want := " และอีก 3 คน"; got[len(got)-len(want):] != want {
		t.Fatalf("got %q", got)
	}
}

// A user's chat from before they ran /tts join stays; only what they wrote
// after opting in is deleted.
func TestCleanup_KeepsMessagesFromBeforeTheUserJoined(t *testing.T) {
	c := newCleanupFixture("a")
	joined := cleanStart.Add(10 * time.Minute)
	c.job.Participants["b"] = joined

	early := c.f.add("t1", "b", joined.Add(-time.Minute))
	late := c.f.add("t1", "b", joined.Add(time.Minute))
	fromA := c.f.add("t1", "a", cleanStart.Add(time.Minute))

	res := c.run()
	if res.Deleted != 2 {
		t.Fatalf("deleted %d, want 2: %+v", res.Deleted, res)
	}
	if c.f.isDeleted(early) {
		t.Error("message from before b joined was deleted")
	}
	if !c.f.isDeleted(late) || !c.f.isDeleted(fromA) {
		t.Error("participant messages after joining were kept")
	}
}
