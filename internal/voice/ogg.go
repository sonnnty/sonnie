package voice

import (
	"context"
	"fmt"

	"github.com/Sonty15/SONNIE/internal/oggopus"
	"github.com/Sonty15/SONNIE/internal/tts"
)

// OggOpusEncoder demuxes an Ogg/Opus bitstream (RFC 3533, RFC 7845) into raw
// Opus audio packets without transcoding or cgo, either from a whole file
// (Encode) or incrementally as the audio streams in (NewStream).
//
// Google Cloud Text-to-Speech emits mono 48 kHz Ogg/Opus (20 ms frames).
// Discord negotiates opus/48000/2 per RFC 7587, and every Opus packet carries
// a self-describing TOC byte (RFC 6716 §3.1) whose stereo bit tells the
// listener's stereo libopus decoder whether the frame is mono or stereo; DAVE
// encrypts the opaque Opus payload unchanged, so mono packets play natively in
// Discord clients.
type OggOpusEncoder struct{}

var _ StreamEncoder = OggOpusEncoder{}

// Encode parses Ogg pages from audio, validates and skips the OpusHead and
// OpusTags headers, and returns the remaining Opus audio packets.
func (e OggOpusEncoder) Encode(ctx context.Context, audio tts.Audio) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := e.NewStream(audio.Format)
	if err != nil {
		return nil, err
	}
	frames, err := s.Write(audio.Data)
	if err != nil {
		return nil, err
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	return frames, nil
}

// NewStream returns a FrameStream that emits the Opus packets of each Ogg
// page as soon as the page is complete.
func (OggOpusEncoder) NewStream(format tts.Format) (FrameStream, error) {
	if format != tts.FormatOggOpus {
		return nil, fmt.Errorf("voice: ogg: unsupported audio format %q (want %q)", format, tts.FormatOggOpus)
	}
	return &oggFrameStream{}, nil
}

type oggFrameStream struct {
	d oggopus.Demuxer
}

func (s *oggFrameStream) Write(chunk []byte) ([][]byte, error) {
	frames, err := s.d.Write(chunk)
	if err != nil {
		return frames, fmt.Errorf("voice: ogg: %w", err)
	}
	return frames, nil
}

func (s *oggFrameStream) Close() error {
	if err := s.d.Close(); err != nil {
		return fmt.Errorf("voice: ogg: %w", err)
	}
	return nil
}
