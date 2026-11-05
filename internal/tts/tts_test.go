package tts

import (
	"context"
	"errors"
	"testing"
)

func TestUnimplemented_ReturnsErrNotImplemented(t *testing.T) {
	_, err := Unimplemented{}.Synthesize(context.Background(), "สวัสดี", "th")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestFake_RecordsRequestsAndEchoesText(t *testing.T) {
	f := &Fake{}
	audio, err := f.Synthesize(context.Background(), "สวัสดี", "th")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if audio.Format != FormatRaw || string(audio.Data) != "สวัสดี" {
		t.Fatalf("unexpected audio: %+v", audio)
	}
	got := f.Requests()
	if len(got) != 1 || got[0] != (Request{Text: "สวัสดี", Lang: "th"}) {
		t.Fatalf("unexpected requests: %+v", got)
	}

	if _, err := f.Synthesize(WithVoice(context.Background(), "Puck"), "ครับ", "th"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got = f.Requests()
	if len(got) != 2 || got[1] != (Request{Text: "ครับ", Lang: "th", Voice: "Puck"}) {
		t.Fatalf("unexpected requests with voice: %+v", got)
	}
}

func TestDefaultVoicePool(t *testing.T) {
	if len(DefaultVoicePool) != 16 {
		t.Fatalf("expected 16 voices in DefaultVoicePool, got %d: %v", len(DefaultVoicePool), DefaultVoicePool)
	}
	seen := make(map[string]bool, len(DefaultVoicePool))
	for _, v := range DefaultVoicePool {
		if v == "" || seen[v] {
			t.Fatalf("invalid or duplicate voice %q in DefaultVoicePool", v)
		}
		seen[v] = true
	}
}

func TestFake_RespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Fake{}).Synthesize(ctx, "x", "th"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
