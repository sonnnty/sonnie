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
