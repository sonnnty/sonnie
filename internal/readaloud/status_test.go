package readaloud

import (
	"testing"
	"time"
)

func TestStatus_NoSession(t *testing.T) {
	e := newEnv(t)
	if got := e.m.Status(); len(got) != 0 {
		t.Fatalf("expected no sessions, got %+v", got)
	}
}

func TestStatus_ReportsParticipantsChannelsAndCounts(t *testing.T) {
	e := twoChannels(t)
	start := e.m.Status()
	if len(start) != 1 {
		t.Fatalf("expected one session, got %+v", start)
	}
	st := start[0]
	if st.GuildID != "g1" || st.VoiceChannelID != "v1" || st.Started.IsZero() {
		t.Fatalf("unexpected session %+v", st)
	}
	if len(st.TextChannels) != 2 || st.TextChannels[0] != "t1" || st.TextChannels[1] != "t2" {
		t.Fatalf("text channels %v", st.TextChannels)
	}
	if len(st.Participants) != 2 || st.Participants[0].UserID != "u1" || st.Participants[0].ChannelID != "t1" ||
		st.Participants[1].UserID != "u2" || st.Participants[1].ChannelID != "t2" {
		t.Fatalf("participants %+v", st.Participants)
	}
	for _, p := range st.Participants {
		if p.Voice == "" {
			t.Fatalf("participant without a voice: %+v", p)
		}
	}

	if _, queued := e.sayIn("t1", "u1", "สวัสดี"); !queued {
		t.Fatal("message not queued")
	}
	// The fake connection buffers the frames, so speech finishes on its own.
	deadline := time.Now().Add(5 * time.Second)
	for e.m.Status()[0].Read != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("read count never reached 1: %+v", e.m.Status()[0])
		}
		time.Sleep(5 * time.Millisecond)
	}
	if q := e.m.Status()[0].Queue; q != 0 {
		t.Fatalf("queue %d after speaking", q)
	}
}
