package voice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

func waitFrame(t *testing.T, c chan []byte) string {
	t.Helper()
	select {
	case f := <-c:
		return string(f)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return ""
	}
}

func TestPlayer_SpeaksInOrder(t *testing.T) {
	synth := &tts.Fake{}
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeEncoder{}, Conn: conn, Lang: "th", QueueSize: 10})
	defer p.Close()

	p.Enqueue("หนึ่ง")
	p.Enqueue("สอง")
	p.Enqueue("สาม")

	for _, want := range []string{"หนึ่ง", "สอง", "สาม"} {
		if got := waitFrame(t, conn.Sent); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
	for _, r := range synth.Requests() {
		if r.Lang != "th" {
			t.Fatalf("expected lang th, got %q", r.Lang)
		}
	}
}

func TestPlayer_EnqueueVoicePassesVoiceToSynthesizer(t *testing.T) {
	synth := &tts.Fake{}
	conn := &FakeConn{Sent: make(chan []byte, 4)}
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeEncoder{}, Conn: conn, Lang: "th", QueueSize: 10})
	defer p.Close()

	p.EnqueueVoice("หนึ่ง", "Puck", nil)
	p.EnqueueVoice("สอง", "Orus", nil)

	for _, want := range []string{"หนึ่ง", "สอง"} {
		if got := waitFrame(t, conn.Sent); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
	reqs := synth.Requests()
	if len(reqs) != 2 || reqs[0].Voice != "Puck" || reqs[1].Voice != "Orus" {
		t.Fatalf("unexpected requests: %+v", reqs)
	}
}

func TestPlayer_ReportsErrorsAndContinues(t *testing.T) {
	errs := make(chan error, 1)
	p := NewPlayer(PlayerConfig{
		Synth:     tts.Unimplemented{},
		Encoder:   FakeEncoder{},
		Conn:      &FakeConn{},
		QueueSize: 10,
		OnError:   func(err error) { errs <- err },
	})
	defer p.Close()

	p.Enqueue("hello")
	select {
	case err := <-errs:
		if !errors.Is(err, tts.ErrNotImplemented) {
			t.Fatalf("expected ErrNotImplemented, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error")
	}
}

func TestPlayer_CloseUnblocksPendingSend(t *testing.T) {
	// Nobody reads Sent, so the first SendOpus blocks until Close.
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: &tts.Fake{}, Encoder: FakeEncoder{}, Conn: conn, QueueSize: 10})
	p.Enqueue("a")
	p.Enqueue("b")

	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
	if conn.Disconnected() {
		t.Fatal("Player must not disconnect the connection it does not own")
	}
}

func TestUnimplementedEncoder(t *testing.T) {
	_, err := UnimplementedEncoder{}.Encode(context.Background(), tts.Audio{})
	if !errors.Is(err, ErrEncoderNotImplemented) {
		t.Fatalf("expected ErrEncoderNotImplemented, got %v", err)
	}
}

func recvDone(t *testing.T, c chan bool) bool {
	t.Helper()
	select {
	case spoken := <-c:
		return spoken
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the done hook")
		return false
	}
}

func TestPlayer_DoneFiresAfterTheLastFrameIsSent(t *testing.T) {
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: &tts.Fake{}, Encoder: FakeEncoder{}, Conn: conn, QueueSize: 10})
	defer p.Close()

	done := make(chan bool, 2)
	p.EnqueueWith("one", func(spoken bool) { done <- spoken })
	p.EnqueueWith("two", func(spoken bool) { done <- spoken })

	// SendOpus blocks until the test takes the frame: playing, not yet played.
	select {
	case <-done:
		t.Fatal("done fired while the frame was still being sent")
	case <-time.After(50 * time.Millisecond):
	}
	if got := waitFrame(t, conn.Sent); got != "one" {
		t.Fatalf("first frame %q", got)
	}
	if !recvDone(t, done) {
		t.Fatal("done(false) for a spoken item")
	}
	select {
	case <-done:
		t.Fatal("second item reported before it was played")
	case <-time.After(50 * time.Millisecond):
	}
	waitFrame(t, conn.Sent)
	if !recvDone(t, done) {
		t.Fatal("done(false) for the second item")
	}
}

func TestPlayer_DoneReportsFailureAndContinues(t *testing.T) {
	p := NewPlayer(PlayerConfig{Synth: tts.Unimplemented{}, Encoder: FakeEncoder{}, Conn: &FakeConn{}, QueueSize: 10})
	defer p.Close()

	done := make(chan bool, 1)
	p.EnqueueWith("x", func(spoken bool) { done <- spoken })
	if recvDone(t, done) {
		t.Fatal("a failed synthesis was reported as spoken")
	}
}

func TestPlayer_DoneReportsDroppedItem(t *testing.T) {
	// The first item blocks in SendOpus, so the rest sit in the queue.
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: &tts.Fake{}, Encoder: FakeEncoder{}, Conn: conn, QueueSize: 1})
	defer p.Close()

	p.Enqueue("playing")
	// The frame is recorded before SendOpus blocks on Sent.
	for deadline := time.Now().Add(2 * time.Second); len(conn.Frames()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the first item never started playing")
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan bool, 2)
	p.EnqueueWith("old", func(spoken bool) { done <- spoken })
	if !p.EnqueueWith("new", nil) {
		t.Fatal("expected the old item to be dropped")
	}
	if recvDone(t, done) {
		t.Fatal("a dropped item was reported as spoken")
	}
}

func TestPlayer_CloseDoesNotReportDiscardedItems(t *testing.T) {
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: &tts.Fake{}, Encoder: FakeEncoder{}, Conn: conn, QueueSize: 10})
	called := make(chan bool, 2)
	p.EnqueueWith("a", func(bool) { called <- true })
	p.EnqueueWith("b", func(bool) { called <- true })
	p.Close()
	select {
	case <-called:
		t.Fatal("done fired for an item cut off by Close")
	case <-time.After(50 * time.Millisecond):
	}
}
