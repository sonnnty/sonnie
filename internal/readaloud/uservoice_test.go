package readaloud

import (
	"slices"
	"strings"
	"testing"
)

func TestHandleUserVoice_DisconnectLeavesTheSession(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	e.mustJoin("u2")
	msgID := e.sessionMessage()

	e.m.HandleUserVoice("g1", "u1", "")

	if got := e.m.Participants("g1"); !slices.Equal(got, []string{"u2"}) {
		t.Fatalf("participants %v", got)
	}
	if d := e.msgr.embed(msgID).Description; strings.Contains(d, "<@u1>") || !strings.Contains(d, "<@u2>") {
		t.Fatalf("card %q", d)
	}
	if _, queued := e.sayIn("t1", "u1", "after leaving voice"); queued {
		t.Fatal("a user who left voice is still read")
	}
}

func TestHandleUserVoice_MoveToAnotherVoiceChannelLeaves(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	e.mustJoin("u2")

	e.m.HandleUserVoice("g1", "u1", "v2")

	if got := e.m.Participants("g1"); !slices.Equal(got, []string{"u2"}) {
		t.Fatalf("participants %v", got)
	}
}

func TestHandleUserVoice_SameChannelAndStrangersChangeNothing(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	before := e.msgr.editCount()

	e.m.HandleUserVoice("g1", "u1", "v1")        // mute, deafen, stream: same channel
	e.m.HandleUserVoice("g1", "stranger", "")    // not in the session
	e.m.HandleUserVoice("other-guild", "u1", "") // no session there

	if got := e.m.Participants("g1"); !slices.Equal(got, []string{"u1"}) {
		t.Fatalf("participants %v", got)
	}
	if e.msgr.editCount() != before {
		t.Fatal("the card was edited")
	}
}

func TestHandleUserVoice_LastParticipantEndsTheSession(t *testing.T) {
	e := newEnv(t)
	e.mustJoin("u1")
	m1, _ := e.sayIn("t1", "u1", "hello")

	e.m.HandleUserVoice("g1", "u1", "")
	e.m.Wait()

	e.assertEnded("g1")
	if !e.msgr.isDeleted(m1) {
		t.Fatal("the end-of-session clean-up did not run")
	}
}
