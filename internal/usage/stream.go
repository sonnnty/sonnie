package usage

import (
	"context"
	"sync"
	"time"

	"github.com/Sonty15/SONNIE/internal/oggopus"
	"github.com/Sonty15/SONNIE/internal/tts"
)

var _ tts.StreamSynthesizer = (*Meter)(nil)

// SynthesizeStream implements tts.StreamSynthesizer with the same limits as
// Synthesize: the character count or the budget is checked before the
// request. In ModeMoney the stream is charged once, when it ends (end of
// audio, an error, or Close), for the audio actually received, so a stream
// cut off by an error or by the bot leaving pays for its partial audio. A
// stream that fails before any audio is not charged, like a failed unary
// request.
//
// An inner synthesizer without a streaming path is used as a one-chunk
// stream.
func (m *Meter) SynthesizeStream(ctx context.Context, text, lang string) (tts.AudioStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.cfg.Mode != ModeMoney {
		if err := m.reserveChars(text); err != nil {
			return nil, err
		}
		return tts.OpenStream(ctx, m.cfg.Inner, text, lang)
	}

	if err := m.checkBudget(); err != nil {
		return nil, err
	}
	inner, err := tts.OpenStream(ctx, m.cfg.Inner, text, lang)
	if err != nil {
		return nil, err
	}
	return &meteredStream{
		AudioStream: inner,
		m:           m,
		inputTokens: EstimateInputTokens(text, m.cfg.Prompt),
	}, nil
}

// meteredStream measures the audio passing through it with an Ogg demuxer
// (granule position minus pre-skip of the complete pages received) and
// charges it once at the end.
type meteredStream struct {
	tts.AudioStream
	m           *Meter
	inputTokens int64

	mu      sync.Mutex
	demux   oggopus.Demuxer
	bad     bool // not Ogg/Opus or malformed: bill by size instead
	bytes   int
	charged bool
}

func (s *meteredStream) Recv() ([]byte, error) {
	chunk, err := s.AudioStream.Recv()
	if len(chunk) > 0 {
		s.count(chunk)
	}
	if err != nil {
		// io.EOF or a failure: either way the stream is over.
		s.charge()
	}
	return chunk, err
}

func (s *meteredStream) Close() error {
	s.charge()
	return s.AudioStream.Close()
}

func (s *meteredStream) count(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.charged {
		return
	}
	s.bytes += len(chunk)
	if s.bad {
		return
	}
	if s.Format() != tts.FormatOggOpus {
		s.bad = true
		return
	}
	if _, err := s.demux.Write(chunk); err != nil {
		s.bad = true
	}
}

func (s *meteredStream) charge() {
	s.mu.Lock()
	if s.charged {
		s.mu.Unlock()
		return
	}
	s.charged = true
	var dur time.Duration
	switch {
	case s.bytes == 0:
		s.mu.Unlock()
		return // no audio arrived: nothing to bill
	case s.bad:
		dur = durationUpperBound(s.bytes)
	default:
		dur = s.demux.Duration()
	}
	s.mu.Unlock()
	s.m.chargeAudio(dur, s.inputTokens)
}
