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
}

func TestFake_RespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Fake{}).Synthesize(ctx, "x", "th"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
