// Package voice plays synthesized speech into a voice connection: a bounded
// per-guild queue, an audio encoder boundary and the connection boundary.
package voice

import (
	"context"
	"errors"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// Discord negotiates Opus at 48 kHz, 2 channels (RFC 7587), 20 ms per frame.
// Individual Opus packets self-describe mono vs stereo in their TOC byte
// (RFC 6716 §3.1), so a receiver's 48 kHz stereo libopus decoder accepts
// both mono and stereo packets directly.
const (
	SampleRate = 48000
	Channels   = 2
	FrameSize  = 960 // samples per channel per 20 ms frame
)

// Encoder converts synthesized audio into Opus frames ready to send to
// Discord, one frame per slice element.
type Encoder interface {
	Encode(ctx context.Context, audio tts.Audio) ([][]byte, error)
}

// Conn is the part of a voice connection the player needs.
type Conn interface {
	// Speaking sets the speaking indicator.
	Speaking(speaking bool) error
	// SendOpus sends one Opus frame, blocking until it is accepted or ctx
	// is done.
	SendOpus(ctx context.Context, frame []byte) error
	// Disconnect leaves the voice channel.
	Disconnect() error
}

// ErrEncoderNotImplemented is returned by UnimplementedEncoder.
var ErrEncoderNotImplemented = errors.New("voice: no audio encoder configured")

// UnimplementedEncoder is the placeholder Encoder used when no synthesizer is
// configured. See OggOpusEncoder for the production Ogg/Opus demuxer.
type UnimplementedEncoder struct{}

func (UnimplementedEncoder) Encode(context.Context, tts.Audio) ([][]byte, error) {
	return nil, ErrEncoderNotImplemented
}
