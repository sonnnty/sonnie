// Package voice plays synthesized speech into a voice connection: a bounded
// per-guild queue, an audio encoder boundary and the connection boundary.
package voice

import (
	"context"
	"errors"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// Discord expects Opus frames of 20 ms at 48 kHz, stereo.
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

// UnimplementedEncoder is the placeholder Encoder.
//
// TODO: implement without cgo. Options, cheapest first:
//   - If the synthesizer returns tts.FormatOggOpus at 48 kHz, demux the Ogg
//     pages into Opus packets in pure Go; no transcoding needed.
//   - Otherwise pipe the audio through an ffmpeg subprocess
//     (-f ogg -c:a libopus -ar 48000 -ac 2 -frame_duration 20) and demux
//     its Ogg output the same way.
//   - A pure-Go Opus encoder would remove the ffmpeg dependency, but none
//     is mature yet; libopus bindings (gopkg.in/hraban/opus.v2) need cgo.
type UnimplementedEncoder struct{}

func (UnimplementedEncoder) Encode(context.Context, tts.Audio) ([][]byte, error) {
	return nil, ErrEncoderNotImplemented
}
