// Package tts defines the text-to-speech boundary. No real engine is wired
// in yet: see Unimplemented and the README for candidate engines.
package tts

import (
	"context"
	"errors"
)

// Format names the encoding of Audio.Data.
type Format string

const (
	// FormatOggOpus is Opus in an Ogg container. Engines that can emit this
	// directly (Google Cloud TTS, Azure) let the voice side skip
	// transcoding and only demux the Ogg pages.
	FormatOggOpus Format = "ogg_opus"
	// FormatMP3 is MPEG-1 Layer III.
	FormatMP3 Format = "mp3"
	// FormatWAV is a RIFF/WAVE file, usually 16-bit PCM.
	FormatWAV Format = "wav"
	// FormatRaw is opaque bytes used by fakes in tests.
	FormatRaw Format = "raw"
)

// Audio is the synthesized speech for one utterance.
type Audio struct {
	Format Format
	Data   []byte
}

// Synthesizer turns text into speech. Lang is a BCP 47 style language code
// such as "th" or "en"; implementations map it to a concrete voice.
type Synthesizer interface {
	Synthesize(ctx context.Context, text, lang string) (Audio, error)
}

// ErrNotImplemented is returned by Unimplemented.
var ErrNotImplemented = errors.New("tts: no synthesizer configured")

// Unimplemented is the placeholder Synthesizer used until a real engine is
// chosen. It always fails with ErrNotImplemented.
//
// TODO: replace with a real engine (see README, "Read chat aloud").
type Unimplemented struct{}

func (Unimplemented) Synthesize(context.Context, string, string) (Audio, error) {
	return Audio{}, ErrNotImplemented
}
