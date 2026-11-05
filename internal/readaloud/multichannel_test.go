package readaloud

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// joinAt makes user a participant read in textCh, in voice channel v1.
func (e *env) joinAt(user, textCh string) JoinResult {
	e.t.Helper()
	res, err := e.join(user, "v1", textCh)
	if err != nil {
		e.t.Fatalf("join %s in %s: %v", user, textCh, err)
	}
	return res
}

// cardIn returns the ID of the one session card the bot posted in channel.
func (e *env) cardIn(channel string) string {
	e.t.Helper()
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	for _, m := range e.msgr.channels[channel] {
		if m.author == e.msgr.botID {
			return m.id
		}
	}
	e.t.Fatalf("no session card in %s", channel)
	return ""
}

// lastBotMessage returns the ID of the newest message the bot posted in channel.
func (e *env) lastBotMessage(channel string) string {
	e.t.Helper()
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	last := ""
	for _, m := range e.msgr.channels[channel] {
		if m.author == e.msgr.botID {
			last = m.id
		}
	}
	if last == "" {
		e.t.Fatalf("no bot message in %s", channel)
	}
	return last
}

func (e *env) cardText(channel string) string {
	e.t.Helper()
	return e.msgr.embed(e.cardIn(channel)).Description
}

// twoChannels starts a session with u1 in t1 and u2 in t2.
func twoChannels(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.joinAt("u1", "t1")
	e.clock.advance(time.Minute)
	e.joinAt("u2", "t2")
	return e
}

func TestMultiChannel_JoinFromTwoChannelsPostsACardInEach(t *testing.T) {
	e := newEnv(t)
	first := e.joinAt("u1", "t1")
	second := e.joinAt("u2", "t2")

	if !first.Started || second.Started || second.AlreadyIn || second.Moved || second.Participants != 2 {
		t.Fatalf("results %+v %+v", first, second)
	}
	if len(e.j.joins) != 1 {
		t.Fatalf("voice joined again: %v", e.j.joins)
	}
	if e.botMessages("t1") != 1 || e.botMessages("t2") != 1 {
		t.Fatalf("cards: t1=%d t2=%d, want one each", e.botMessages("t1"), e.botMessages("t2"))
	}
	want := "👥 <@u1> · <#t1>\n<@u2> · <#t2>\n"
	for _, ch := range []string{"t1", "t2"} {
		if got := e.cardText(ch); !strings.HasPrefix(got, want) {
			t.Errorf("card in %s: %q, want it to start with %q", ch, got, want)
		}
		mk := e.liveMarker(e.cardIn(ch))
		if len(mk.Channels) != 2 || mk.Reading["u1"] != "t1" || mk.Reading["u2"] != "t2" || len(mk.Binds) != 2 {
			t.Errorf("marker in %s: %+v", ch, mk)
		}
	}
	if got := e.m.TextChannels("g1"); len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("text channels %v", got)
	}
	// The first card was edited once to list the second channel; the new card was posted up to date.
	if n := e.msgr.editCount(); n != 1 {
		t.Fatalf("%d edits, want 1", n)
	}

	// A third participant joining in t2 adds to t2's group and edits both cards, posting nothing.
	e.joinAt("u3", "t2")
	if e.botMessages("t2") != 1 || e.msgr.editCount() != 3 {
		t.Fatalf("cards in t2: %d, edits %d", e.botMessages("t2"), e.msgr.editCount())
	}
	if got := e.cardText("t1"); !strings.HasPrefix(got, "👥 <@u1> · <#t1>\n<@u2> <@u3> · <#t2>\n") {
		t.Fatalf("card %q", got)
	}
}

func TestMultiChannel_OnlyMessagesInTheParticipantsChannelAreRead(t *testing.T) {
	e := twoChannels(t)

	if _, queued := e.sayIn("t1", "u1", "สวัสดี"); !queued {
		t.Fatal("u1 in t1 was not read")
	}
	if got, want := recv(t, e.j.conn(0)), "name-u1: สวัสดี"; got != want {
		t.Fatalf("spoke %q, want %q", got, want)
	}
	if _, queued := e.sayIn("t2", "u2", "ครับ"); !queued {
		t.Fatal("u2 in t2 was not read")
	}
	if got, want := recv(t, e.j.conn(0)), "name-u2: ครับ"; got != want {
		t.Fatalf("spoke %q, want %q", got, want)
	}
	crossU1, q1 := e.sayIn("t2", "u1", "wrong channel")
	crossU2, q2 := e.sayIn("t1", "u2", "wrong channel")
	_, q3 := e.sayIn("t3", "u1", "unbound channel")
	_, q4 := e.sayIn("t2", "stranger", "hello")
	if q1 || q2 || q3 || q4 {
		t.Fatalf("queued cross-channel/unbound/stranger messages: %v %v %v %v", q1, q2, q3, q4)
	}
	// Not read, and (no per-message deletion here) not touched while the session runs.
	if e.msgr.isDeleted(crossU1) || e.msgr.isDeleted(crossU2) {
		t.Fatal("a message in another channel was deleted")
	}
}

func TestMultiChannel_MovingChangesTheReadChannel(t *testing.T) {
	e := newEnv(t)
	e.joinAt("u1", "t1")
	e.joinAt("u2", "t1")
	e.clock.advance(time.Minute)

	res := e.joinAt("u1", "t3")

	if !res.Moved || res.PreviousChannelID != "t1" || res.AlreadyIn || res.Started || res.Participants != 2 {
		t.Fatalf("result %+v", res)
	}
	if ch, _ := e.m.TextChannel("g1", "u1"); ch != "t3" {
		t.Fatalf("u1 is read in %q", ch)
	}
	if e.botMessages("t3") != 1 {
		t.Fatal("moving to a new channel must post a card there")
	}
	if _, queued := e.sayIn("t1", "u1", "old channel"); queued {
		t.Fatal("u1 is still read in t1 after moving")
	}
	if _, queued := e.sayIn("t3", "u1", "new channel"); !queued {
		t.Fatal("u1 is not read in t3 after moving")
	}
	if _, queued := e.sayIn("t1", "u2", "unaffected"); !queued {
		t.Fatal("u2 stopped being read")
	}
	for _, ch := range []string{"t1", "t3"} {
		if got := e.cardText(ch); !strings.HasPrefix(got, "👥 <@u2> · <#t1>\n<@u1> · <#t3>\n") {
			t.Errorf("card in %s: %q", ch, got)
		}
	}

	// Moving back finds the old card and posts nothing new.
	res = e.joinAt("u1", "t1")
	if !res.Moved || res.PreviousChannelID != "t3" || e.botMessages("t1") != 1 || e.botMessages("t3") != 1 {
		t.Fatalf("move back: %+v, cards t1=%d t3=%d", res, e.botMessages("t1"), e.botMessages("t3"))
	}
	// The card of a channel nobody is read in any more stays and says so by omission.
	if got := e.cardText("t3"); !strings.HasPrefix(got, "👥 <@u1> <@u2> · <#t1>\n🔈") {
		t.Fatalf("card %q", got)
	}
	if res := e.joinAt("u1", "t1"); !res.AlreadyIn {
		t.Fatalf("repeat in the same channel: %+v", res)
	}
}

func TestMultiChannel_FailedCardLeavesTheSessionAsItWas(t *testing.T) {
	e := newEnv(t)
	e.joinAt("u1", "t1")
	edits := e.msgr.editCount()
	e.msgr.postErr = errors.New("missing send messages")

	_, err := e.join("u2", "v1", "t2")

	if !errors.Is(err, ErrSessionMessage) {
		t.Fatalf("expected ErrSessionMessage, got %v", err)
	}
	if got := e.m.Participants("g1"); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("participants %v", got)
	}
	if ch := e.m.TextChannels("g1"); len(ch) != 1 || e.msgr.editCount() != edits || e.j.conn(0).Disconnected() {
		t.Fatalf("channels %v, edits %d, disconnected %v: the session must be untouched", ch, e.msgr.editCount(), e.j.conn(0).Disconnected())
	}
	// A move that fails keeps the user where they were.
	e.msgr.postErr = nil
	e.joinAt("u2", "t1")
	e.msgr.postErr = errors.New("missing send messages")
	if _, err := e.join("u2", "v1", "t4"); !errors.Is(err, ErrSessionMessage) {
		t.Fatalf("expected ErrSessionMessage, got %v", err)
	}
	if ch, _ := e.m.TextChannel("g1", "u2"); ch != "t1" {
		t.Fatalf("u2 is read in %q, want t1", ch)
	}
}

func TestMultiChannel_LeaveEditsEveryCard(t *testing.T) {
	e := twoChannels(t)
	before := e.msgr.editCount()

	if res, err := e.m.Leave("g1", "u2"); err != nil || res.Ended || res.Participants != 1 {
		t.Fatalf("leave: %+v %v", res, err)
	}

	if e.msgr.editCount() != before+2 {
		t.Fatalf("%d edits, want one per card", e.msgr.editCount()-before)
	}
	for _, ch := range []string{"t1", "t2"} {
		if got := e.cardText(ch); !strings.HasPrefix(got, "👥 <@u1> · <#t1>\n🔈") || strings.Contains(got, "u2") {
			t.Errorf("card in %s: %q", ch, got)
		}
		mk := e.liveMarker(e.cardIn(ch))
		if len(mk.Left) != 1 || mk.Left[0] != "u2" || len(mk.Channels) != 2 || len(mk.Binds) != 2 {
			t.Errorf("marker in %s: %+v", ch, mk)
		}
	}
	// A user who left is no longer read in t2, but may come back.
	if _, queued := e.sayIn("t2", "u2", "left"); queued {
		t.Fatal("a user who left was read")
	}
	if res := e.joinAt("u2", "t2"); res.Moved || res.AlreadyIn || e.botMessages("t2") != 1 {
		t.Fatalf("rejoin %+v", res)
	}
}

func TestMultiChannel_EndSummarisesEveryCard(t *testing.T) {
	e := twoChannels(t)
	c1, c2 := e.cardIn("t1"), e.cardIn("t2")
	mine1, _ := e.sayIn("t1", "u1", "a")
	mine2, _ := e.sayIn("t2", "u2", "b")
	e.clock.advance(10 * time.Minute)

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	res, err := e.m.Leave("g1", "u2")
	if err != nil || !res.Ended {
		t.Fatalf("leave: %+v %v", res, err)
	}

	for _, id := range []string{c1, c2} {
		got := e.summaryOf(id)
		for _, want := range []string{"จบการอ่าน", "<@u1> <@u2>", "ลบ 2 ข้อความ"} {
			if !strings.Contains(got, want) {
				t.Errorf("summary %q missing %q", got, want)
			}
		}
	}
	if !e.msgr.isDeleted(mine1) || !e.msgr.isDeleted(mine2) || e.msgr.isDeleted(c1) || e.msgr.isDeleted(c2) {
		t.Fatal("wrong messages deleted")
	}
	e.assertEnded("g1")
}

func TestMultiChannel_CleanupPerChannelIncludingAMovedUser(t *testing.T) {
	e := newEnv(t)
	e.joinAt("u1", "t1")
	a1, _ := e.sayIn("t1", "u1", "a1")
	e.clock.advance(time.Minute)
	e.joinAt("u2", "t2")
	// u1 wrote in t2 before they were bound there, and in t3, which they never use: both stay.
	early, _ := e.sayIn("t2", "u1", "before binding t2")
	elsewhere, _ := e.sayIn("t3", "u1", "never bound")
	e.clock.advance(time.Minute)
	e.joinAt("u1", "t2") // moves
	c1, _ := e.sayIn("t2", "u1", "c1")
	// After the move t1 is no longer read, but it was bound: this goes at the end.
	d1, _ := e.sayIn("t1", "u1", "d1")
	e2, _ := e.sayIn("t2", "u2", "e2")
	s1, _ := e.sayIn("t1", "stranger", "s1")
	s2, _ := e.sayIn("t2", "stranger", "s2")
	// u2 leaves; what they write in t2 afterwards is still theirs to clean up.
	if _, err := e.m.Leave("g1", "u2"); err != nil {
		t.Fatal(err)
	}
	late2, _ := e.sayIn("t2", "u2", "after leaving")
	e.clock.advance(time.Minute)
	if res, err := e.m.Leave("g1", "u1"); err != nil || !res.Ended {
		t.Fatalf("leave: %+v %v", res, err)
	}
	afterEnd := e.msgr.add("t2", "u1", e.clock.advance(time.Second))

	got := e.summaryOf(e.cardIn("t1"))

	for name, id := range map[string]string{"a1": a1, "c1": c1, "d1": d1, "e2": e2, "late2": late2} {
		if !e.msgr.isDeleted(id) {
			t.Errorf("%s was not deleted", name)
		}
	}
	for name, id := range map[string]string{
		"before binding": early, "unbound channel": elsewhere, "stranger t1": s1, "stranger t2": s2,
		"after the end": afterEnd, "card t1": e.cardIn("t1"), "card t2": e.cardIn("t2"),
	} {
		if e.msgr.isDeleted(id) {
			t.Errorf("%s must stay", name)
		}
	}
	if !strings.Contains(got, "ลบ 5 ข้อความ") {
		t.Fatalf("summary %q", got)
	}
	if e.summaryOf(e.cardIn("t2")) != got {
		t.Fatal("all cards carry the same summary")
	}
}

func TestMultiChannel_CleanupScansEachChannelAfterItsOwnCard(t *testing.T) {
	// Messages of the first user in the second channel from before its card
	// existed belong to nobody's clean-up, even with the "from the card" zero time.
	e := newEnv(t)
	e.joinAt("u1", "t1")
	e.clock.advance(time.Minute)
	e.joinAt("u2", "t2")
	missed := e.msgr.add("t2", "u2", e.clock.advance(time.Second)) // never reached HandleMessage

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Leave("g1", "u2"); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()

	if !e.msgr.isDeleted(missed) {
		t.Fatal("the history scan of t2 did not find the message")
	}
}

func TestMultiChannel_ScanFailureFallsBackToTrackedMessagesOfEachChannel(t *testing.T) {
	e := twoChannels(t)
	m1, _ := e.sayIn("t1", "u1", "x")
	m2, _ := e.sayIn("t2", "u2", "y")
	cross, _ := e.sayIn("t2", "u1", "z") // never bound in t2: not tracked
	e.msgr.scanErr = errors.New("no Read Message History")

	e.m.Leave("g1", "u1")
	e.m.Leave("g1", "u2")
	e.m.Wait()

	if !e.msgr.isDeleted(m1) || !e.msgr.isDeleted(m2) || e.msgr.isDeleted(cross) {
		t.Fatalf("deleted m1=%v m2=%v cross=%v", e.msgr.isDeleted(m1), e.msgr.isDeleted(m2), e.msgr.isDeleted(cross))
	}
}

func TestMultiChannel_ManagePermissionIsPerChannel(t *testing.T) {
	e := newEnv(t)
	e.msgr.noManage = map[string]bool{"t2": true}
	e.joinAt("u1", "t1")
	res := e.joinAt("u2", "t2")
	if res.CanManage {
		t.Fatal("CanManage must describe t2")
	}
	if e.msgr.embed(e.cardIn("t1")).Color != colorActive || e.msgr.embed(e.cardIn("t2")).Color != colorWarning {
		t.Fatal("only the card in t2 must warn")
	}
	if mk := e.liveMarker(e.cardIn("t1")); !mk.Channels[0].Manage || mk.Channels[1].Manage {
		t.Fatalf("marker %+v", mk.Channels)
	}
	m1, _ := e.sayIn("t1", "u1", "a")
	m2, _ := e.sayIn("t2", "u2", "b")
	e.m.Leave("g1", "u1")
	e.m.Leave("g1", "u2")

	got := e.summaryOf(e.cardIn("t1"))

	if !e.msgr.isDeleted(m1) || e.msgr.isDeleted(m2) {
		t.Fatalf("deleted t1=%v t2=%v", e.msgr.isDeleted(m1), e.msgr.isDeleted(m2))
	}
	if !strings.Contains(got, "ลบ 1 ข้อความ") || strings.Contains(got, "ไม่ได้ลบ") {
		t.Fatalf("summary %q", got)
	}
}

func TestMultiChannel_AutoLeaveEndsEveryCard(t *testing.T) {
	e := twoChannels(t)
	m1, _ := e.sayIn("t1", "u1", "a")
	m2, _ := e.sayIn("t2", "u2", "b")

	e.p.setHumans("v1", 0)
	e.m.HandleVoiceStateUpdate("g1")

	e.summaryOf(e.cardIn("t1"))
	e.summaryOf(e.cardIn("t2"))
	if !e.msgr.isDeleted(m1) || !e.msgr.isDeleted(m2) || !e.j.conn(0).Disconnected() {
		t.Fatal("auto-leave did not clean up both channels")
	}
	e.assertEnded("g1")
}

func TestMultiChannel_AutoDeleteWorksInEveryChannel(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.joinAt("u1", "t1")
	e.joinAt("u2", "t2")
	e.clock.advance(time.Second)
	a, _ := e.sayIn("t1", "u1", "one")
	b, _ := e.sayIn("t2", "u2", "two")
	filtered, _ := e.sayIn("t2", "u2", "<:pepe:1>")
	cross, _ := e.sayIn("t2", "u1", "not mine here")
	waitUntil(t, "three timers", func() bool { return ft.scheduled() == 3 })

	ft.fireAll()

	for name, id := range map[string]string{"t1": a, "t2": b, "t2 filtered": filtered} {
		if !e.msgr.isDeleted(id) {
			t.Errorf("message in %s was not deleted", name)
		}
	}
	if e.msgr.isDeleted(cross) {
		t.Error("a participant's message in another channel must not be auto-deleted")
	}
	// The end-of-session clean-up still takes what auto-delete left, and counts every deletion once.
	msg := e.cardIn("t1")
	e.m.Leave("g1", "u1")
	e.m.Leave("g1", "u2")
	if got := e.summaryOf(msg); !strings.Contains(got, "ลบ 3 ข้อความ") {
		t.Fatalf("summary %q", got)
	}
	if e.msgr.isDeleted(cross) {
		t.Error("u1 was never bound in t2: its message there stays")
	}
}

func TestMultiChannel_AutoDeleteOnlyWhereTheBotMayDelete(t *testing.T) {
	e, ft := newDeleteEnv(t)
	e.msgr.noManage = map[string]bool{"t2": true}
	e.joinAt("u1", "t1")
	e.joinAt("u2", "t2")
	a, _ := e.sayIn("t1", "u1", "one")
	b, _ := e.sayIn("t2", "u2", "two")
	waitUntil(t, "a timer", func() bool { return ft.scheduled() == 1 })

	ft.fireAll()

	if !e.msgr.isDeleted(a) || e.msgr.isDeleted(b) || len(e.singles()) != 1 {
		t.Fatalf("t1 deleted=%v t2 deleted=%v singles=%v", e.msgr.isDeleted(a), e.msgr.isDeleted(b), e.singles())
	}
}

func TestMultiChannel_CloseCleansEveryChannel(t *testing.T) {
	e := twoChannels(t)
	m1, _ := e.sayIn("t1", "u1", "a")
	m2, _ := e.sayIn("t2", "u2", "b")

	e.m.Close()

	e.summaryOf(e.cardIn("t1"))
	e.summaryOf(e.cardIn("t2"))
	if !e.msgr.isDeleted(m1) || !e.msgr.isDeleted(m2) {
		t.Fatal("shutdown left messages behind")
	}
}

// openCard writes view's card for channel into the fake as if a previous run
// had posted it, and returns the OpenSession a history scan would report.
func (e *env) openCard(view sessionView, channel string, at time.Time) (OpenSession, string) {
	e.t.Helper()
	e.msgr.mu.Lock()
	id := e.msgr.addLocked(channel, e.msgr.botID, at, sessionEmbed(view, channel))
	e.msgr.mu.Unlock()
	return OpenSession{GuildID: "g1", ChannelID: channel, MessageID: id, Time: at, Marker: view.marker(true)}, id
}

// lostSession is a v2 session that a previous run left open: u1 in t1 from
// the start, u2 in t2 from +1 min, and u1 moved to t2 at +5 min.
func lostSession(e *env) (view sessionView, cards []OpenSession, ids map[string]string) {
	start := e.clock.now()
	view = sessionView{VoiceChannelID: "v1", Started: start}.
		join("u1", "t1", true, time.Time{}).
		join("u2", "t2", true, start.Add(time.Minute)).
		join("u1", "t2", true, start.Add(5*time.Minute))
	c1, id1 := e.openCard(view, "t1", start)
	c2, id2 := e.openCard(view, "t2", start.Add(time.Minute))
	return view, []OpenSession{c1, c2}, map[string]string{"t1": id1, "t2": id2}
}

func TestMultiChannel_RecoveryFindsBothCards(t *testing.T) {
	e := newEnv(t)
	start := e.clock.now()
	_, cards, ids := lostSession(e)
	// t1: u1's message before and after moving (both go), a stranger's (stays).
	t1a := e.msgr.add("t1", "u1", start.Add(2*time.Minute))
	t1b := e.msgr.add("t1", "u1", start.Add(6*time.Minute))
	t1s := e.msgr.add("t1", "stranger", start.Add(3*time.Minute))
	// t2: u2 from +1min; u1 only from +5min, so +2min stays; u3 is unrelated.
	t2u2 := e.msgr.add("t2", "u2", start.Add(2*time.Minute))
	t2early := e.msgr.add("t2", "u1", start.Add(3*time.Minute))
	t2u1 := e.msgr.add("t2", "u1", start.Add(7*time.Minute))
	t2s := e.msgr.add("t2", "stranger", start.Add(8*time.Minute))
	e.clock.advance(20 * time.Minute)
	e.msgr.open = map[string][]OpenSession{"g1": cards}

	e.m.RecoverGuild(context.Background(), "g1")
	got := e.summaryOf(ids["t1"])

	for name, id := range map[string]string{"t1a": t1a, "t1b": t1b, "t2u2": t2u2, "t2u1": t2u1} {
		if !e.msgr.isDeleted(id) {
			t.Errorf("%s was not deleted", name)
		}
	}
	for name, id := range map[string]string{"t1 stranger": t1s, "t2 early": t2early, "t2 stranger": t2s, "card t1": ids["t1"], "card t2": ids["t2"]} {
		if e.msgr.isDeleted(id) {
			t.Errorf("%s must stay", name)
		}
	}
	for _, want := range []string{"<@u1> <@u2>", "ลบ 4 ข้อความ", "20 นาที"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
	if e.summaryOf(ids["t2"]) != got {
		t.Fatal("both cards get the same summary")
	}
	// Nothing runs twice: a later scan sees ended cards only.
	e.msgr.open = nil
	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()
	if n := e.msgr.editCount(); n != 2 {
		t.Fatalf("%d edits, want one summary per card", n)
	}
}

func TestMultiChannel_RecoveryMergesStaleCards(t *testing.T) {
	// A failed edit left the card in t1 with the marker of the first join only.
	e := newEnv(t)
	start := e.clock.now()
	old := sessionView{VoiceChannelID: "v1", Started: start}.join("u1", "t1", true, time.Time{})
	oldCard, id1 := e.openCard(old, "t1", start)
	fresh := old.join("u2", "t2", true, start.Add(time.Minute))
	freshCard, id2 := e.openCard(fresh, "t2", start.Add(time.Minute))
	m2 := e.msgr.add("t2", "u2", start.Add(2*time.Minute))
	e.clock.advance(5 * time.Minute)
	e.msgr.open = map[string][]OpenSession{"g1": {oldCard, freshCard}}

	e.m.RecoverGuild(context.Background(), "g1")
	got := e.summaryOf(id1)

	e.summaryOf(id2)
	if !e.msgr.isDeleted(m2) || !strings.Contains(got, "<@u1> <@u2>") {
		t.Fatalf("deleted=%v summary %q", e.msgr.isDeleted(m2), got)
	}
}

func TestMultiChannel_RecoverySkipsChannelsWithoutAFoundCard(t *testing.T) {
	e := newEnv(t)
	start := e.clock.now()
	_, cards, ids := lostSession(e)
	m1 := e.msgr.add("t1", "u1", start.Add(2*time.Minute))
	m2 := e.msgr.add("t2", "u2", start.Add(2*time.Minute))
	e.clock.advance(time.Hour)
	e.msgr.open = map[string][]OpenSession{"g1": cards[:1]} // the card in t2 is beyond the scanned history

	e.m.RecoverGuild(context.Background(), "g1")
	e.summaryOf(ids["t1"])

	if !e.msgr.isDeleted(m1) || e.msgr.isDeleted(m2) {
		t.Fatalf("t1 deleted=%v t2 deleted=%v", e.msgr.isDeleted(m1), e.msgr.isDeleted(m2))
	}
}

func TestMultiChannel_RecoveryStopsAtALiveSessionsCardInTheSameChannel(t *testing.T) {
	e := newEnv(t)
	start := e.clock.now()
	_, cards, ids := lostSession(e)
	oldT1 := e.msgr.add("t1", "u1", start.Add(2*time.Minute))
	oldT2 := e.msgr.add("t2", "u2", start.Add(2*time.Minute))
	e.clock.advance(time.Hour)
	// A new session starts after the restart and has its card in t1 only.
	e.joinAt("u1", "t1")
	liveT1, _ := e.sayIn("t1", "u1", "live")
	liveT2 := e.msgr.add("t2", "u2", e.clock.advance(time.Second)) // u2 is not in the live session
	// The scan of t1 finds the live card too (same voice channel as the
	// lost session), as discordMessenger.OpenSessions would.
	liveCard := e.lastBotMessage("t1")
	if liveCard == ids["t1"] {
		t.Fatal("no live card was posted in t1")
	}
	live := OpenSession{GuildID: "g1", ChannelID: "t1", MessageID: liveCard, Time: e.clock.now(), Marker: e.liveMarker(liveCard)}
	e.msgr.open = map[string][]OpenSession{"g1": {live, cards[0], cards[1]}}

	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()

	if !e.msgr.isDeleted(oldT1) || !e.msgr.isDeleted(oldT2) {
		t.Fatal("the old session's messages must go")
	}
	if e.msgr.isDeleted(liveT1) {
		t.Fatal("recovery deleted a message of the live session in t1")
	}
	if !e.msgr.isDeleted(liveT2) {
		t.Fatal("t2 is not part of the live session: the old session still owns it")
	}
	if _, ok := e.m.Bound("g1"); !ok {
		t.Fatal("the live session was ended")
	}
	_ = ids
}

func TestMultiChannel_RecoveryLeavesALiveSessionAlone(t *testing.T) {
	e := twoChannels(t)
	e.sayIn("t1", "u1", "live")
	var open []OpenSession
	for _, ch := range []string{"t1", "t2"} {
		id := e.cardIn(ch)
		open = append(open, OpenSession{GuildID: "g1", ChannelID: ch, MessageID: id, Time: e.clock.now(), Marker: e.liveMarker(id)})
	}
	e.msgr.open = map[string][]OpenSession{"g1": open}

	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()

	if len(e.msgr.bulk)+len(e.msgr.single) != 0 {
		t.Fatal("a live session was cleaned up by recovery")
	}
	if _, ok := e.m.Bound("g1"); !ok {
		t.Fatal("live session was ended")
	}
}

func TestMultiChannel_RecoveryOfV1AndV2SessionsSideBySide(t *testing.T) {
	e := newEnv(t)
	start := e.clock.now()
	v1 := e.msgr.add("t9", "bot", start)
	m1 := e.msgr.add("t9", "u9", start.Add(time.Minute))
	_, cards, ids := lostSession(e)
	m2 := e.msgr.add("t1", "u1", start.Add(time.Minute))
	e.clock.advance(time.Hour)
	e.msgr.open = map[string][]OpenSession{"g1": {
		{GuildID: "g1", ChannelID: "t9", MessageID: v1, Time: start,
			Marker: SessionMarker{Version: 1, Open: true, Started: start, Voice: "v7", Manage: true, Participants: []string{"u9"}}},
		cards[0], cards[1],
	}}

	e.m.RecoverGuild(context.Background(), "g1")
	e.summaryOf(v1)
	e.summaryOf(ids["t1"])

	if !e.msgr.isDeleted(m1) || !e.msgr.isDeleted(m2) {
		t.Fatal("both sessions must be cleaned")
	}
	if got := e.msgr.embed(v1).Description; strings.Contains(got, "u1") || !strings.Contains(got, "<@u9>") {
		t.Fatalf("the v1 session was merged with the v2 one: %q", got)
	}
}
