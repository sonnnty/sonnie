// Package tts defines the text-to-speech boundary and its Google Cloud
// Text-to-Speech implementation.
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

// Unimplemented is the placeholder Synthesizer used when no engine is
// configured (for example when GOOGLE_TTS_API_KEY is unset). It always fails
// with ErrNotImplemented.
type Unimplemented struct{}

func (Unimplemented) Synthesize(context.Context, string, string) (Audio, error) {
	return Audio{}, ErrNotImplemented
}

// DefaultVoicePool lists the Gemini-TTS male voices handed out to session
// participants when TTS_VOICE_POOL is not set.
var DefaultVoicePool = []string{
	"Achird",
	"Algenib",
	"Algieba",
	"Alnilam",
	"Charon",
	"Enceladus",
	"Fenrir",
	"Iapetus",
	"Orus",
	"Puck",
	"Rasalgethi",
	"Sadachbia",
	"Sadaltager",
	"Schedar",
	"Umbriel",
	"Zubenelgenubi",
}

// FemaleVoices lists the Gemini-TTS female voices. They are never handed out
// at random; participants can pick one with /tts join voice:.
var FemaleVoices = []string{
	"Achernar",
	"Aoede",
	"Autonoe",
	"Callirrhoe",
	"Despina",
	"Erinome",
	"Gacrux",
	"Kore",
	"Laomedeia",
	"Leda",
	"Pulcherrima",
	"Sulafat",
	"Vindemiatrix",
	"Zephyr",
}

type voiceContextKey struct{}

// WithVoice attaches a per-utterance speaker voice override to ctx. An empty
// voice leaves ctx unchanged so synthesizers fall back to their configured
// default voice.
func WithVoice(ctx context.Context, voice string) context.Context {
	if voice == "" {
		return ctx
	}
	return context.WithValue(ctx, voiceContextKey{}, voice)
}

// VoiceFromContext returns the per-utterance speaker voice attached to ctx by
// WithVoice, or "" if none was set.
func VoiceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(voiceContextKey{}).(string)
	return v
}
