package readaloud

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// fakeTimers is a controllable Config.AfterFunc: nothing fires until a test
// says so.
type fakeTimers struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
	fired   bool
	owner   *fakeTimers
}

func (t *fakeTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

func (ft *fakeTimers) after(d time.Duration, f func()) Timer {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{d: d, f: f, owner: ft}
	ft.timers = append(ft.timers, t)
	return t
}

// scheduled counts every timer ever set; active only those still waiting.
func (ft *fakeTimers) scheduled() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.timers)
}

func (ft *fakeTimers) active() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	n := 0
	for _, t := range ft.timers {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

// fireAll runs every timer that is still waiting, like the clock passing the
// delay.
func (ft *fakeTimers) fireAll() {
	ft.mu.Lock()
	var run []*fakeTimer
	for _, t := range ft.timers {
		if !t.stopped && !t.fired {
			t.fired = true
			run = append(run, t)
		}
	}
	ft.mu.Unlock()
	for _, t := range run {
		t.f()
	}
}

// fireStale runs the callback of every timer regardless of its state: a timer
// that was already firing when Stop was called.
func (ft *fakeTimers) fireStale() {
	ft.mu.Lock()
	timers := append([]*fakeTimer(nil), ft.timers...)
	ft.mu.Unlock()
	for _, t := range timers {
		t.f()
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func newDeleteEnv(t *testing.T) (*env, *fakeTimers) {
	t.Helper()
	e := newEnv(t)
	ft := &fakeTimers{}
	e.m.cfg.DeleteAfter = 8 * time.Second
	e.m.cfg.AfterFunc = ft.after
	return e, ft
}

func (e *env) singles() []string {
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	return append([]string(nil), e.msgr.single...)
}

func TestAutoDelete_OnlyAfterPlaybackFinishedAndTheDelay(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.j.blocking = true
	e.mustJoin("u1")

	id, queued := e.say("u1", "สวัสดี")
	if !queued {
		t.Fatal("message was not queued")
	}
	// The frame is being sent: playing, not yet played.
	time.Sleep(50 * time.Millisecond)
	if ft.scheduled() != 0 {
		t.Fatal("a deletion was scheduled while the message was still being spoken")
	}

	if got := recv(t, e.j.conn(0)); got != "name-u1: สวัสดี" {
		t.Fatalf("spoke %q", got)
	}
	waitUntil(t, "the deletion to be scheduled", func() bool { return ft.scheduled() == 1 })
	if d := ft.timers[0].d; d != 8*time.Second {
		t.Fatalf("delay %v, want 8s", d)
	}
	if e.msgr.isDeleted(id) || len(e.singles()) != 0 {
		t.Fatal("the message was deleted before the delay passed")
	}

	ft.fireAll()
	if !e.msgr.isDeleted(id) {
		t.Fatal("the message was not deleted after the delay")
	}
	if got := e.singles(); len(got) != 1 || got[0] != id || len(e.msgr.bulk) != 0 {
		t.Fatalf("single deletes %v, bulk %v; want one single delete of %s", got, e.msgr.bulk, id)
	}
}

func TestAutoDelete_OneTimerPerMessageInOrder(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	a, _ := e.say("u1", "one")
	b, _ := e.say("u1", "two")
	waitUntil(t, "both timers", func() bool { return ft.scheduled() == 2 })

	ft.fireAll()

	if got := e.singles(); len(got) != 2 {
		t.Fatalf("deleted %v", got)
	}
	if !e.msgr.isDeleted(a) || !e.msgr.isDeleted(b) {
		t.Fatal("both messages should be gone")
	}
}

func TestAutoDelete_ZeroDelayTurnsItOff(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.m.cfg.DeleteAfter = 0
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	id, _ := e.say("u1", "hello")
	e.say("u1", "<:pepe:1>")
	recv(t, e.j.conn(0))
	time.Sleep(20 * time.Millisecond)

	if ft.scheduled() != 0 || len(e.singles()) != 0 || e.msgr.isDeleted(id) {
		t.Fatal("per-message deletion ran with TTS_DELETE_AFTER=0")
	}
	// The end-of-session clean-up still takes everything.
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)
	if !e.msgr.isDeleted(id) || !strings.Contains(got, "2 ข้อความ") {
		t.Fatalf("clean-up result: deleted=%v summary %q", e.msgr.isDeleted(id), got)
	}
}

func TestAutoDelete_NoPermissionNoDeletes(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.msgr.canManage = false
	e.mustJoin("u1")
	id, _ := e.say("u1", "hello")
	e.say("u1", "<:pepe:1>")
	recv(t, e.j.conn(0))
	time.Sleep(20 * time.Millisecond)

	if ft.scheduled() != 0 || len(e.singles()) != 0 || e.msgr.isDeleted(id) {
		t.Fatal("deleted without Manage Messages")
	}
}

func TestAutoDelete_FilteredMessagesAreDeletedToo(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	e.mustJoin("u2")
	if _, err := e.m.Leave("g1", "u2"); err != nil {
		t.Fatal(err)
	}

	emojiOnly, q1 := e.say("u1", "<:pepe:1>")
	blank, q2 := e.say("u1", "   ")
	stranger, _ := e.say("stranger", "hi")
	left, _ := e.say("u2", "I left")
	if q1 || q2 {
		t.Fatal("filtered messages must not be queued")
	}
	// Only the participant's two filtered messages get a timer: nothing for a
	// stranger (not ours to touch), nor for someone who already left.
	if ft.scheduled() != 2 {
		t.Fatalf("%d timers, want 2", ft.scheduled())
	}
	ft.fireAll()
	for _, id := range []string{emojiOnly, blank} {
		if !e.msgr.isDeleted(id) {
			t.Errorf("filtered message %s stays", id)
		}
	}
	for _, id := range []string{stranger, left} {
		if e.msgr.isDeleted(id) {
			t.Errorf("message %s was deleted early", id)
		}
	}
}

func TestAutoDelete_SynthesisErrorStillDeletes(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.m.cfg.Synth = &tts.Fake{Err: errors.New("quota")}
	e.mustJoin("u1")
	id, queued := e.say("u1", "will not be spoken")
	if !queued {
		t.Fatal("expected the message to be queued")
	}
	waitUntil(t, "the deletion to be scheduled", func() bool { return ft.scheduled() == 1 })
	ft.fireAll()
	if !e.msgr.isDeleted(id) {
		t.Fatal("a message the engine could not speak should still go")
	}
}

func TestAutoDelete_DroppedFromFullQueueStillDeletes(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.m.cfg.QueueSize = 1
	e.j.blocking = true
	e.mustJoin("u1")

	first, _ := e.say("u1", "playing")
	waitUntil(t, "playback to start", func() bool { return len(e.j.conn(0).Frames()) == 1 })
	dropped, _ := e.say("u1", "queued")
	e.say("u1", "pushes the previous one out")

	// Only the dropped message is done; the playing and the newest are not.
	if ft.scheduled() != 1 {
		t.Fatalf("%d timers, want 1 (the dropped message)", ft.scheduled())
	}
	ft.fireAll()
	if got := e.singles(); len(got) != 1 || got[0] != dropped {
		t.Fatalf("deleted %v, want only %s (not %s)", got, dropped, first)
	}
}

func TestAutoDelete_FailureLogsOnceAndLeavesItToTheEndCleanup(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	id, _ := e.say("u1", "hello")
	waitUntil(t, "the deletion to be scheduled", func() bool { return ft.scheduled() == 1 })

	e.msgr.deleteErr = errors.New("rate limited")
	ft.fireAll()
	if e.msgr.isDeleted(id) {
		t.Fatal("delete should have failed")
	}
	if n := strings.Count(buf.String(), "after it was read"); n != 1 {
		t.Fatalf("failure logged %d times, want 1: %q", n, buf.String())
	}
	e.m.mu.Lock()
	_, tracked := e.m.sessions["g1"].tracked[id]
	e.m.mu.Unlock()
	if !tracked {
		t.Fatal("a failed message must stay tracked")
	}

	e.msgr.deleteErr = nil
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)
	if !e.msgr.isDeleted(id) || !strings.Contains(got, "1 ข้อความ") {
		t.Fatalf("end clean-up: deleted=%v summary %q", e.msgr.isDeleted(id), got)
	}
	// The failed live delete is retried: two attempts in all, no more.
	if n := len(e.singles()); n != 2 {
		t.Fatalf("%d delete attempts, want 2", n)
	}
}

func TestAutoDelete_ScanFailureFallsBackToWhatIsStillTracked(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	gone, _ := e.say("u1", "deleted live")
	kept, _ := e.say("u1", "failed live")
	waitUntil(t, "both timers", func() bool { return ft.scheduled() == 2 })

	// Only the first delete succeeds.
	ft.mu.Lock()
	first, second := ft.timers[0], ft.timers[1]
	first.fired, second.fired = true, true
	ft.mu.Unlock()
	first.f()
	e.msgr.mu.Lock()
	e.msgr.deleteErr = errors.New("boom")
	e.msgr.mu.Unlock()
	second.f()
	e.msgr.mu.Lock()
	e.msgr.deleteErr = nil
	e.msgr.scanErr = errors.New("history unavailable")
	e.msgr.mu.Unlock()

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)
	if !e.msgr.isDeleted(gone) || !e.msgr.isDeleted(kept) {
		t.Fatal("both messages should be deleted by now")
	}
	// The live delete is not tracked any more, so the fallback does not try it
	// again: 1 live + 1 retry by the clean-up, and the summary counts both.
	if n := len(e.singles()); n != 3 {
		t.Fatalf("%d delete calls (%v), want 3", n, e.singles())
	}
	if !strings.Contains(got, "2 ข้อความ") {
		t.Fatalf("summary %q should count 2 deleted messages", got)
	}
}

func TestAutoDelete_SummaryCountsLiveAndEndDeletesOnce(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	for _, text := range []string{"one", "two"} {
		e.say("u1", text)
	}
	waitUntil(t, "both timers", func() bool { return ft.scheduled() == 2 })
	ft.fireAll()
	// A third is written, spoken, and not yet due when the session ends.
	third, _ := e.say("u1", "three")
	waitUntil(t, "the third timer", func() bool { return ft.scheduled() == 3 })

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)

	if !strings.Contains(got, "3 ข้อความ") {
		t.Fatalf("summary %q should count 3 deleted messages", got)
	}
	if !e.msgr.isDeleted(third) {
		t.Fatal("the pending message was not taken by the clean-up")
	}
	if n := len(e.singles()); n != 3 {
		t.Fatalf("%d delete calls (%v), want 3 (2 live + 1 at the end)", n, e.singles())
	}
}

func TestAutoDelete_SessionEndCancelsPendingTimers(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	id, _ := e.say("u1", "hello")
	waitUntil(t, "the timer", func() bool { return ft.scheduled() == 1 })

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if ft.active() != 0 {
		t.Fatal("a timer outlived the session")
	}
	got := e.summaryOf(msgID)
	// A callback that was already running when the timer was stopped does
	// nothing: no second delete, no double count.
	ft.fireStale()
	if n := len(e.singles()); n != 1 {
		t.Fatalf("%d delete calls, want 1 (the clean-up's)", n)
	}
	if !e.msgr.isDeleted(id) || !strings.Contains(got, "1 ข้อความ") {
		t.Fatalf("deleted=%v summary %q", e.msgr.isDeleted(id), got)
	}
}

func TestAutoDelete_NothingScheduledAfterTheSessionEnded(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	s := e.m.sessions["g1"]
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()
	// The player reports a message that finished just as the session ended.
	e.m.scheduleDelete(s, "t1", "m999")
	if ft.scheduled() != 0 {
		t.Fatal("a timer was set on an ended session")
	}
}

func TestAutoDelete_CloseCancelsPendingTimers(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	id, _ := e.say("u1", "hello")
	waitUntil(t, "the timer", func() bool { return ft.scheduled() == 1 })

	e.m.Close()

	if ft.active() != 0 {
		t.Fatal("a timer outlived Close")
	}
	ft.fireStale()
	got := e.summaryOf(msgID)
	if n := len(e.singles()); n != 1 || !e.msgr.isDeleted(id) || !strings.Contains(got, "1 ข้อความ") {
		t.Fatalf("calls %v, deleted=%v, summary %q", e.singles(), e.msgr.isDeleted(id), got)
	}
}

// The live session embed must say messages go after being read when
// per-message deletion is on, and only at the end when it is off.
func TestAutoDelete_SessionEmbedFooterSaysWhenMessagesGo(t *testing.T) {
	for _, tc := range []struct {
		after time.Duration
		want  string
	}{
		{8 * time.Second, "ลบข้อความหลังอ่าน"},
		{0, "ลบข้อความเมื่อจบ"},
	} {
		e, _ := newDeleteEnv(t)
		e.m.cfg.DeleteAfter = tc.after
		e.mustJoin("u1")
		emb := e.msgr.embed(e.sessionMessage())
		if emb == nil || emb.Footer == nil || !strings.Contains(emb.Footer.Text, tc.want) {
			t.Errorf("DeleteAfter=%v: footer %+v, want it to contain %q", tc.after, emb.Footer, tc.want)
		}
	}
}
