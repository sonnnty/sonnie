package readaloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/voice"
)

type fakeJoiner struct {
	mu       sync.Mutex
	err      error
	joins    []string // "guild/channel"
	conns    []*voice.FakeConn
	presence *fakePresence
	onJoin   func(guildID, channelID string)
}

func (j *fakeJoiner) Join(_ context.Context, guildID, channelID string) (voice.Conn, error) {
	j.mu.Lock()
	if j.err != nil {
		j.mu.Unlock()
		return nil, j.err
	}
	j.joins = append(j.joins, guildID+"/"+channelID)
	c := &voice.FakeConn{Sent: make(chan []byte, 16)}
	j.conns = append(j.conns, c)
	p := j.presence
	onJoin := j.onJoin
	j.mu.Unlock()

	if p != nil {
		p.setBotChannel(channelID)
	}
	if onJoin != nil {
		onJoin(guildID, channelID)
	}
	return c, nil
}

func (j *fakeJoiner) conn(i int) *voice.FakeConn {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.conns[i]
}

type fakePresence struct {
	mu         sync.Mutex
	humans     map[string]int // channel -> humans
	botChannel string
}

func (p *fakePresence) HumansInVoice(_, channelID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.humans[channelID]
}

func (p *fakePresence) BotVoiceChannel(string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.botChannel
}

func (p *fakePresence) setBotChannel(channelID string) {
	p.mu.Lock()
	p.botChannel = channelID
	p.mu.Unlock()
}

func (p *fakePresence) setHumans(channelID string, n int) {
	p.mu.Lock()
	p.humans[channelID] = n
	p.mu.Unlock()
}

// fakeClock is a settable clock shared by the Manager and the fake Messenger.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

// env wires a Manager to fakes. Guild g1 has voice channels v1 and v2 and text
// channel t1 unless a test says otherwise.
type env struct {
	t        *testing.T
	m        *Manager
	j        *fakeJoiner
	p        *fakePresence
	msgr     *fakeMessenger
	clock    *fakeClock
	sessions int // session messages posted so far
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	j := &fakeJoiner{}
	p := &fakePresence{humans: map[string]int{"v1": 2, "v2": 2}}
	j.presence = p
	msgr := newFakeMessenger()
	msgr.clock = clock.now
	m := New(Config{
		Synth:       &tts.Fake{},
		Encoder:     voice.FakeEncoder{},
		Joiner:      j,
		Presence:    p,
		Messenger:   msgr,
		Lang:        "th",
		MaxChars:    200,
		QueueSize:   10,
		SpeakAuthor: true,
		Now:         clock.now,
	})
	t.Cleanup(m.Close)
	return &env{t: t, m: m, j: j, p: p, msgr: msgr, clock: clock}
}

func (e *env) join(user, voiceCh, textCh string) (JoinResult, error) {
	e.t.Helper()
	return e.m.Join(context.Background(), JoinRequest{GuildID: "g1", UserID: user, VoiceChannelID: voiceCh, TextChannelID: textCh})
}

func (e *env) mustJoin(user string) JoinResult {
	e.t.Helper()
	res, err := e.join(user, "v1", "t1")
	if err != nil {
		e.t.Fatalf("join %s: %v", user, err)
	}
	return res
}

// say posts a message in t1 as author and feeds it to the Manager the way the
// bot would. It returns the message ID and whether it was queued for speech.
func (e *env) say(author, content string) (string, bool) {
	return e.sayIn("t1", author, content)
}

func (e *env) sayIn(channel, author, content string) (string, bool) {
	e.t.Helper()
	at := e.clock.advance(time.Second)
	id := e.msgr.add(channel, author, at)
	queued := e.m.HandleMessage(Message{
		GuildID: "g1", ChannelID: channel, MessageID: id, Time: at,
		AuthorID: author, AuthorName: "name-" + author, Content: content,
	}, nil)
	return id, queued
}

// sessionMessage returns the ID of the one session message posted in t1.
func (e *env) sessionMessage() string {
	e.t.Helper()
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	var ids []string
	for _, m := range e.msgr.channels["t1"] {
		if m.author == e.msgr.botID {
			ids = append(ids, m.id)
		}
	}
	if len(ids) == 0 {
		e.t.Fatal("no session message was posted")
	}
	return ids[0]
}

func (e *env) botMessages(channel string) int {
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	n := 0
	for _, m := range e.msgr.channels[channel] {
		if m.author == e.msgr.botID {
			n++
		}
	}
	return n
}

// summaryOf waits for background clean-ups and returns the session message's
// summary text, failing if the message is not an ended-session summary.
func (e *env) summaryOf(messageID string) string {
	e.t.Helper()
	e.m.Wait()
	emb := e.msgr.embed(messageID)
	if emb == nil {
		e.t.Fatal("session message has no embed")
	}
	mk, ok := ParseSessionMarkerFromEmbed(emb)
	if !ok || mk.Open {
		e.t.Fatalf("session message is not a summary: %q (marker %+v ok=%v)", emb.Description, mk, ok)
	}
	return emb.Description
}

func (e *env) liveMarker(messageID string) SessionMarker {
	e.t.Helper()
	mk, ok := ParseSessionMarkerFromEmbed(e.msgr.embed(messageID))
	if !ok || !mk.Open {
		e.t.Fatalf("session message is not open: %+v ok=%v", mk, ok)
	}
	return mk
}

func (e *env) assertEnded(guild string) {
	e.t.Helper()
	if _, _, ok := e.m.Bound(guild); ok {
		e.t.Fatal("session still active")
	}
}

func recv(t *testing.T, c *voice.FakeConn) string {
	t.Helper()
	select {
	case f := <-c.Sent:
		return string(f)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for speech")
		return ""
	}
}

func TestManager_FirstJoinStartsSessionWithOneMessage(t *testing.T) {
	e := newEnv(t)

	res := e.mustJoin("u1")

	if !res.Started || res.AlreadyIn || res.Participants != 1 || !res.CanManage {
		t.Fatalf("result %+v", res)
	}
	if len(e.j.joins) != 1 || e.j.joins[0] != "g1/v1" {
		t.Fatalf("voice joins %v", e.j.joins)
	}
	if text, vc, ok := e.m.Bound("g1"); !ok || text != "t1" || vc != "v1" {
		t.Fatalf("unexpected session: %q %q %v", text, vc, ok)
	}
	if n := e.botMessages("t1"); n != 1 {
		t.Fatalf("%d session messages posted, want 1", n)
	}
	emb := e.msgr.embed(e.sessionMessage())
	if !strings.Contains(emb.Description, "<@u1>") || emb.Color != colorActive {
		t.Fatalf("session embed %+v", emb)
	}
	mk := e.liveMarker(e.sessionMessage())
	if mk.Voice != "v1" || !mk.Manage || len(mk.Participants) != 1 {
		t.Fatalf("marker %+v", mk)
	}
}

func TestManager_OnlyParticipantsAreReadAloud(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")

	if _, queued := e.say("u1", "สวัสดี"); !queued {
		t.Fatal("participant message was not queued")
	}
	if got, want := recv(t, e.j.conn(0)), "name-u1: สวัสดี"; got != want {
		t.Fatalf("spoke %q, want %q", got, want)
	}

	stranger, queued := e.say("stranger", "ไม่เกี่ยวกับฉัน")
	if queued {
		t.Fatal("a non-participant's message was queued")
	}
	if _, queued := e.sayIn("t2", "u1", "wrong channel"); queued {
		t.Fatal("a message in another channel was queued")
	}
	// The stranger's message is neither spoken nor deleted when the session ends.
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()
	if e.msgr.isDeleted(stranger) {
		t.Fatal("a non-participant's message was deleted")
	}
}

func TestManager_IgnoresBotsAndEmptyFromParticipants(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")

	tests := []struct {
		name string
		msg  Message
	}{
		{"other guild", Message{GuildID: "g2", ChannelID: "t1", AuthorID: "u1", Content: "hi"}},
		{"bot", Message{GuildID: "g1", ChannelID: "t1", AuthorID: "u1", Content: "hi", IsBot: true}},
		{"empty", Message{GuildID: "g1", ChannelID: "t1", AuthorID: "u1", Content: ""}},
		{"empty after normalising", Message{GuildID: "g1", ChannelID: "t1", AuthorID: "u1", Content: "<:pepe:1>"}},
		{"unknown author", Message{GuildID: "g1", ChannelID: "t1", Content: "hi"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if e.m.HandleMessage(tt.msg, nil) {
				t.Fatal("expected message to be ignored")
			}
		})
	}
}

func TestManager_LaterJoinEditsTheSameMessage(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()

	res := e.mustJoin("u2")

	if res.Started || res.AlreadyIn || res.Participants != 2 {
		t.Fatalf("result %+v", res)
	}
	if n := e.botMessages("t1"); n != 1 {
		t.Fatalf("%d bot messages, want the one session message", n)
	}
	if len(e.j.joins) != 1 {
		t.Fatalf("voice joined again: %v", e.j.joins)
	}
	if e.msgr.editCount() != 1 {
		t.Fatalf("%d edits, want 1", e.msgr.editCount())
	}
	emb := e.msgr.embed(msgID)
	if !strings.Contains(emb.Description, "<@u1> <@u2>") || !strings.Contains(emb.Description, "(2)") {
		t.Fatalf("description %q", emb.Description)
	}
	if mk := e.liveMarker(msgID); len(mk.Participants) != 2 {
		t.Fatalf("marker %+v", mk)
	}

	// The new participant is read aloud from now on.
	if _, queued := e.say("u2", "ผมมาแล้ว"); !queued {
		t.Fatal("new participant's message was not queued")
	}
}

func TestManager_JoiningTwiceChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	res := e.mustJoin("u1")
	if !res.AlreadyIn || res.Started || res.Participants != 1 {
		t.Fatalf("result %+v", res)
	}
	if e.msgr.editCount() != 0 || e.botMessages("t1") != 1 {
		t.Fatal("a repeated join must not edit or post")
	}
}

func TestManager_JoinRefusals(t *testing.T) {
	t.Run("other voice channel with humans", func(t *testing.T) {
		e := newEnv(t)
		e.mustJoin("u1")

		_, err := e.join("u2", "v2", "t1")

		var busy *BusyError
		if !errors.As(err, &busy) || busy.VoiceChannelID != "v1" || busy.TextChannelID != "t1" {
			t.Fatalf("expected BusyError for v1/t1, got %v", err)
		}
		if _, vc, _ := e.m.Bound("g1"); vc != "v1" || len(e.j.joins) != 1 {
			t.Fatal("the existing session must be left alone")
		}
		if got := e.m.Participants("g1"); len(got) != 1 || got[0] != "u1" {
			t.Fatalf("participants %v", got)
		}
		if e.msgr.editCount() != 0 {
			t.Fatal("a refused join must not edit the session message")
		}
	})
	t.Run("unknown occupancy counts as busy", func(t *testing.T) {
		e := newEnv(t)
		e.m.cfg.Presence = nil
		e.mustJoin("u1")
		if _, err := e.join("u2", "v2", "t1"); !errors.As(err, new(*BusyError)) {
			t.Fatalf("expected BusyError, got %v", err)
		}
	})
	t.Run("other text channel", func(t *testing.T) {
		e := newEnv(t)
		e.mustJoin("u1")

		_, err := e.join("u2", "v1", "t2")

		var wrong *TextChannelError
		if !errors.As(err, &wrong) || wrong.TextChannelID != "t1" {
			t.Fatalf("expected TextChannelError naming t1, got %v", err)
		}
		if got := e.m.Participants("g1"); len(got) != 1 {
			t.Fatalf("participants %v: the refused user must not be added", got)
		}
		if e.msgr.editCount() != 0 || len(e.msgr.channels["t2"]) != 0 {
			t.Fatal("a refused join must not edit or post")
		}
	})
}

func TestManager_JoinOtherVoiceChannelReplacesEmptySession(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	old := e.sessionMessage()
	mine, _ := e.say("u1", "ก่อนย้าย")
	e.p.setHumans("v1", 0) // everyone left v1 without the bot noticing yet

	res, err := e.join("u2", "v2", "t2")
	if err != nil || !res.Started {
		t.Fatalf("join: %+v %v", res, err)
	}

	if !e.j.conn(0).Disconnected() {
		t.Fatal("old connection was not disconnected")
	}
	if text, vc, _ := e.m.Bound("g1"); text != "t2" || vc != "v2" {
		t.Fatalf("session is %q/%q, want t2/v2", text, vc)
	}
	// The old session was cleaned up and summarised; the new one has its own message.
	if !strings.Contains(e.summaryOf(old), "ลบข้อความแล้ว 1 ข้อความ") || !e.msgr.isDeleted(mine) {
		t.Fatal("the old session was not cleaned up")
	}
	if e.botMessages("t2") != 1 {
		t.Fatalf("new session message missing in t2")
	}
	if got := e.m.Participants("g1"); len(got) != 1 || got[0] != "u2" {
		t.Fatalf("participants %v, want only u2", got)
	}
}

func TestManager_JoinError(t *testing.T) {
	e := newEnv(t)
	want := errors.New("no permission")
	e.j.err = want
	if _, err := e.join("u1", "v1", "t1"); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
	e.assertEnded("g1")
	if e.botMessages("t1") != 0 {
		t.Fatal("a session message was posted although joining voice failed")
	}
}

func TestManager_FailedSessionMessageLeavesVoiceAgain(t *testing.T) {
	e := newEnv(t)
	e.msgr.postErr = errors.New("missing send messages")

	_, err := e.join("u1", "v1", "t1")

	if !errors.Is(err, ErrSessionMessage) {
		t.Fatalf("expected ErrSessionMessage, got %v", err)
	}
	e.assertEnded("g1")
	if !e.j.conn(0).Disconnected() {
		t.Fatal("bot stayed in voice without a session")
	}
}

func TestManager_LeaveRemovesParticipantAndEditsMessage(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	e.mustJoin("u2")
	msgID := e.sessionMessage()
	before := e.msgr.editCount()

	res, err := e.m.Leave("g1", "u1")

	if err != nil || res.Ended || res.Participants != 1 {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if e.msgr.editCount() != before+1 {
		t.Fatal("leaving must edit the session message once")
	}
	emb := e.msgr.embed(msgID)
	if strings.Contains(emb.Description, "<@u1>") || !strings.Contains(emb.Description, "<@u2>") {
		t.Fatalf("description %q", emb.Description)
	}
	if mk := e.liveMarker(msgID); len(mk.Participants) != 1 || len(mk.Left) != 1 || mk.Left[0] != "u1" {
		t.Fatalf("marker %+v", mk)
	}
	if _, queued := e.say("u1", "ออกแล้ว"); queued {
		t.Fatal("a user who left is still read aloud")
	}
	if _, queued := e.say("u2", "ยังอยู่"); !queued {
		t.Fatal("the remaining participant is not read aloud")
	}
	if _, _, ok := e.m.Bound("g1"); !ok || e.j.conn(0).Disconnected() {
		t.Fatal("the session must continue while participants remain")
	}
}

func TestManager_LeaveErrors(t *testing.T) {
	e := newEnv(t)
	if _, err := e.m.Leave("g1", "u1"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("expected ErrNotBound, got %v", err)
	}
	e.mustJoin("u1")
	if _, err := e.m.Leave("g1", "u9"); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("expected ErrNotParticipant, got %v", err)
	}
	if got := e.m.Participants("g1"); len(got) != 1 {
		t.Fatalf("participants %v", got)
	}
}

func TestManager_LastLeaveEndsSessionWithCleanup(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	e.clock.advance(24 * time.Minute)
	mine1, _ := e.say("u1", "หนึ่ง")
	e.mustJoin("u2")
	mine2, _ := e.say("u2", "สอง")
	stranger, _ := e.say("stranger", "คนนอก")
	// u1 leaves but their messages were posted during the session and go too.
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	late, _ := e.say("u1", "หลังออก")
	e.clock.advance(time.Minute)
	res, err := e.m.Leave("g1", "u2")
	if err != nil || !res.Ended {
		t.Fatalf("result %+v err %v", res, err)
	}
	afterEnd := e.msgr.add("t1", "u1", e.clock.advance(time.Second))

	got := e.summaryOf(msgID)

	e.assertEnded("g1")
	if !e.j.conn(0).Disconnected() {
		t.Fatal("expected disconnect on the last leave")
	}
	for _, id := range []string{mine1, mine2, late} {
		if !e.msgr.isDeleted(id) {
			t.Errorf("participant message %s was not deleted", id)
		}
	}
	for name, id := range map[string]string{"non-participant": stranger, "session message": msgID, "after the end": afterEnd} {
		if e.msgr.isDeleted(id) {
			t.Errorf("%s message must stay", name)
		}
	}
	for _, want := range []string{"จบการอ่านออกเสียง", "<@u1>", "<@u2>", "25 นาที", "ลบข้อความแล้ว 3 ข้อความ"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
	if _, queued := e.say("u2", "x"); queued {
		t.Fatal("speech continued after the session ended")
	}
}

func TestManager_SessionEndTriggers(t *testing.T) {
	tests := []struct {
		name           string
		trigger        func(e *env)
		wantDisconnect bool
	}{
		{"no humans left in voice", func(e *env) { e.p.setHumans("v1", 0); e.m.HandleVoiceStateUpdate("g1") }, true},
		{"bot disconnected", func(e *env) { e.p.setBotChannel(""); e.m.HandleVoiceStateUpdate("g1") }, false},
		{"bot moved to another channel", func(e *env) { e.p.setBotChannel("v2"); e.m.HandleVoiceStateUpdate("g1") }, true},
		{"shutdown", func(e *env) { e.m.Close() }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.mustJoin("u1")
			msgID := e.sessionMessage()
			mine, _ := e.say("u1", "hello")
			stranger, _ := e.say("stranger", "hello")

			tt.trigger(e)

			if got := e.summaryOf(msgID); !strings.Contains(got, "ลบข้อความแล้ว 1 ข้อความ") {
				t.Fatalf("summary %q", got)
			}
			e.assertEnded("g1")
			if !e.msgr.isDeleted(mine) || e.msgr.isDeleted(stranger) {
				t.Fatal("wrong messages deleted")
			}
			if got := e.j.conn(0).Disconnected(); got != tt.wantDisconnect {
				t.Fatalf("disconnected = %v, want %v", got, tt.wantDisconnect)
			}
		})
	}
}

func TestManager_KeepsSessionWhileHumansRemain(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	e.m.HandleVoiceStateUpdate("g1")
	if _, _, ok := e.m.Bound("g1"); !ok {
		t.Fatal("ended while humans were still in the channel")
	}
	if e.msgr.editCount() != 0 {
		t.Fatal("unexpected edit")
	}
}

func TestManager_KeepsSessionWhenMoveCausedByJoin(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	e.p.setHumans("v1", 0)
	// Simulate Discord firing VoiceStateUpdate both while /tts join is in
	// flight and after it completes.
	e.j.onJoin = func(guildID, _ string) { e.m.HandleVoiceStateUpdate(guildID) }

	if _, err := e.join("u2", "v2", "t2"); err != nil {
		t.Fatalf("second join: %v", err)
	}
	e.m.HandleVoiceStateUpdate("g1")

	if text, vc, ok := e.m.Bound("g1"); !ok || text != "t2" || vc != "v2" {
		t.Fatalf("expected session (t2, v2, true), got (%q, %q, %v)", text, vc, ok)
	}
	if !e.j.conn(0).Disconnected() || e.j.conn(1).Disconnected() {
		t.Fatal("expected only the old connection to be disconnected")
	}
	if _, queued := e.sayIn("t2", "u2", "สวัสดี"); !queued {
		t.Fatal("expected message in the new text channel to be queued")
	}
	if got, want := recv(t, e.j.conn(1)), "name-u2: สวัสดี"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestManager_CloseEndsEverySessionAndCleansUp(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msg1 := e.sessionMessage()
	mine, _ := e.say("u1", "hi")
	if _, err := e.m.Join(context.Background(), JoinRequest{GuildID: "g2", UserID: "u9", VoiceChannelID: "v9", TextChannelID: "t9"}); err != nil {
		t.Fatal(err)
	}

	e.m.Close()

	for i := 0; i < 2; i++ {
		if !e.j.conn(i).Disconnected() {
			t.Fatalf("connection %d not disconnected", i)
		}
	}
	e.summaryOf(msg1)
	if !e.msgr.isDeleted(mine) {
		t.Fatal("participant message survived shutdown")
	}
	if e.botMessages("t9") != 1 {
		t.Fatal("second guild's session message missing")
	}
	if mk := e.liveMarkerOrSummary("t9"); mk.Open {
		t.Fatal("second guild's session was not summarised")
	}
}

// liveMarkerOrSummary parses the marker of the only bot message in channel.
func (e *env) liveMarkerOrSummary(channel string) SessionMarker {
	e.t.Helper()
	e.msgr.mu.Lock()
	defer e.msgr.mu.Unlock()
	for _, m := range e.msgr.channels[channel] {
		if m.author == e.msgr.botID {
			mk, ok := ParseSessionMarkerFromEmbed(m.embed)
			if !ok {
				e.t.Fatalf("no marker on %s", m.id)
			}
			return mk
		}
	}
	e.t.Fatal("no bot message")
	return SessionMarker{}
}

func TestManager_MissingManageMessagesAtStart(t *testing.T) {
	e := newEnv(t)
	e.msgr.canManage = false

	res := e.mustJoin("u1")

	if res.CanManage {
		t.Fatal("CanManage must be false")
	}
	msgID := e.sessionMessage()
	emb := e.msgr.embed(msgID)
	if emb.Color != colorWarning || !strings.Contains(emb.Fields[len(emb.Fields)-1].Value, "Manage Messages") {
		t.Fatalf("session message does not warn: %+v", emb)
	}
	mine, _ := e.say("u1", "hello")
	// Permission granted later: participants were told nothing is deleted, so nothing is.
	e.msgr.mu.Lock()
	e.msgr.canManage = true
	e.msgr.mu.Unlock()

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)

	if e.msgr.isDeleted(mine) || len(e.msgr.single)+len(e.msgr.bulk) != 0 {
		t.Fatal("messages were deleted without the permission at the start")
	}
	if !strings.Contains(got, "ไม่ได้ลบข้อความ") || !strings.Contains(got, "Manage Messages") {
		t.Fatalf("summary %q", got)
	}
}

func TestManager_PermissionLostDuringSession(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	mine, _ := e.say("u1", "hello")
	e.msgr.mu.Lock()
	e.msgr.canManage = false
	e.msgr.mu.Unlock()

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)

	if e.msgr.isDeleted(mine) || !strings.Contains(got, "ไม่ได้ลบข้อความ") {
		t.Fatalf("deleted=%v summary %q", e.msgr.isDeleted(mine), got)
	}
}

func TestManager_CleanupFindsMessagesTheBotNeverSaw(t *testing.T) {
	// A dropped gateway event: the message is in history but was never
	// handed to HandleMessage.
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	missed := e.msgr.add("t1", "u1", e.clock.advance(time.Second))

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	e.summaryOf(msgID)

	if !e.msgr.isDeleted(missed) {
		t.Fatal("message found only by the history scan was not deleted")
	}
}

func TestManager_CleanupFallsBackToTrackedMessagesWhenScanFails(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	mine, _ := e.say("u1", "hello")
	stranger, _ := e.say("stranger", "hello")
	e.msgr.scanErr = errors.New("no Read Message History")

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	got := e.summaryOf(msgID)

	if !e.msgr.isDeleted(mine) || e.msgr.isDeleted(stranger) || !strings.Contains(got, "ลบข้อความแล้ว 1 ข้อความ") {
		t.Fatalf("deleted mine=%v stranger=%v summary %q", e.msgr.isDeleted(mine), e.msgr.isDeleted(stranger), got)
	}
}

func TestManager_SummaryMentionsFailedDeletes(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	e.say("u1", "one")
	e.msgr.bulkErr = errors.New("boom")
	e.msgr.deleteErr = errors.New("forbidden")

	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if got := e.summaryOf(msgID); !strings.Contains(got, "ลบไม่ได้ 1") {
		t.Fatalf("summary %q", got)
	}
}

func TestManager_RecoverFinishesUnfinishedSession(t *testing.T) {
	e := newEnv(t)
	// A session from before a restart: the bot's open session message, then
	// messages by two participants (one had left) and by a stranger.
	started := e.clock.now()
	sessionMsg := e.msgr.add("t1", "bot", started)
	mk := SessionMarker{Open: true, Started: started, Voice: "v1", Manage: true, Participants: []string{"u1"}, Left: []string{"u2"}}
	e.msgr.mu.Lock()
	e.msgr.find(sessionMsg).embed = sessionEmbed(sessionView{
		TextChannelID: "t1", VoiceChannelID: "v1", Started: started, Manage: true,
		Participants: mk.Participants, Left: mk.Left,
	})
	e.msgr.mu.Unlock()
	m1 := e.msgr.add("t1", "u1", e.clock.advance(time.Minute))
	m2 := e.msgr.add("t1", "u2", e.clock.advance(time.Minute))
	stranger := e.msgr.add("t1", "stranger", e.clock.advance(time.Minute))
	e.clock.advance(10 * time.Minute)
	e.msgr.open = map[string][]OpenSession{"g1": {
		{GuildID: "g1", ChannelID: "t1", MessageID: sessionMsg, Time: started, Marker: mk},
	}}

	e.m.RecoverGuild(context.Background(), "g1")
	got := e.summaryOf(sessionMsg)

	if !e.msgr.isDeleted(m1) || !e.msgr.isDeleted(m2) || e.msgr.isDeleted(stranger) || e.msgr.isDeleted(sessionMsg) {
		t.Fatalf("wrong messages deleted; alive: %v", e.msgr.alive("t1"))
	}
	for _, want := range []string{"<@u1>", "<@u2>", "ลบข้อความแล้ว 2 ข้อความ", "13 นาที"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}

	// A second scan finds nothing to do: the message is a summary now.
	e.msgr.open = nil
	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()
	if len(e.msgr.bulk)+len(e.msgr.single) != 1 { // m1+m2 in one bulk call, nothing more
		t.Fatalf("clean-up ran again: bulk %v single %v", e.msgr.bulk, e.msgr.single)
	}
}

func TestManager_RecoverTakesOverEachMessageOnce(t *testing.T) {
	e := newEnv(t)
	started := e.clock.now()
	id := e.msgr.add("t1", "bot", started)
	o := OpenSession{GuildID: "g1", ChannelID: "t1", MessageID: id, Time: started,
		Marker: SessionMarker{Open: true, Started: started, Manage: true, Participants: []string{"u1"}}}
	e.msgr.open = map[string][]OpenSession{"g1": {o}}
	e.msgr.embedFor(id, sessionEmbed(sessionView{TextChannelID: "t1", Started: started, Manage: true}))
	e.msgr.add("t1", "u1", e.clock.advance(time.Second))

	e.m.RecoverGuild(context.Background(), "g1")
	e.m.RecoverGuild(context.Background(), "g1") // e.g. a second GuildCreate before the first finished
	e.m.Wait()

	if n := e.msgr.editCount(); n != 1 {
		t.Fatalf("%d summary edits, want 1", n)
	}
}

func TestManager_RecoverLeavesLiveSessionAlone(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	msgID := e.sessionMessage()
	e.say("u1", "live")
	e.msgr.open = map[string][]OpenSession{"g1": {
		{GuildID: "g1", ChannelID: "t1", MessageID: msgID, Time: e.clock.now(),
			Marker: SessionMarker{Open: true, Started: e.clock.now(), Manage: true, Participants: []string{"u1"}}},
	}}

	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()

	if len(e.msgr.bulk)+len(e.msgr.single) != 0 {
		t.Fatal("a live session was cleaned up by recovery")
	}
	if _, _, ok := e.m.Bound("g1"); !ok {
		t.Fatal("live session was ended")
	}
}

func TestManager_RecoverStopsAtTheStartOfALiveSessionInTheSameChannel(t *testing.T) {
	e := newEnv(t)
	started := e.clock.now()
	old := e.msgr.add("t1", "bot", started)
	e.msgr.embedFor(old, sessionEmbed(sessionView{TextChannelID: "t1", Started: started, Manage: true}))
	oldMsg := e.msgr.add("t1", "u1", e.clock.advance(time.Minute))
	e.clock.advance(time.Minute)

	// A new session in the same channel starts before recovery runs.
	e.mustJoin("u1")
	newMsg, _ := e.say("u1", "new session")
	e.msgr.open = map[string][]OpenSession{"g1": {
		{GuildID: "g1", ChannelID: "t1", MessageID: old, Time: started,
			Marker: SessionMarker{Open: true, Started: started, Manage: true, Participants: []string{"u1"}}},
	}}

	e.m.RecoverGuild(context.Background(), "g1")
	e.m.Wait()

	if !e.msgr.isDeleted(oldMsg) {
		t.Fatal("the old session's message should be deleted")
	}
	if e.msgr.isDeleted(newMsg) {
		t.Fatal("recovery deleted a message of the live session")
	}
}

func TestManager_LogsLimitErrorOncePerMonth(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	m := New(Config{})
	nov := fmt.Errorf("synthesize: %w", &usage.LimitError{Month: "2026-11", Used: 10, Limit: 10})
	for i := 0; i < 5; i++ {
		if !m.logLimitOnce(nov) {
			t.Fatal("limit error should be handled")
		}
	}
	if n := strings.Count(buf.String(), "limit reached"); n != 1 {
		t.Fatalf("logged %d times, want 1: %q", n, buf.String())
	}
	dec := fmt.Errorf("synthesize: %w", &usage.LimitError{Month: "2026-12", Used: 10, Limit: 10})
	m.logLimitOnce(dec)
	if n := strings.Count(buf.String(), "limit reached"); n != 2 {
		t.Fatalf("expected a new log in the next month, got %d", n)
	}
	if m.logLimitOnce(errors.New("other")) {
		t.Fatal("other errors must not be swallowed")
	}
}
