package tts

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"time"
)

// AudioStream is speech that arrives in chunks while it is being
// synthesized. Concatenating every chunk gives a complete file in Format.
type AudioStream interface {
	Format() Format
	// Recv returns the next chunk of audio, or io.EOF after the last one.
	// Any other error ends the stream; the chunks before it are valid.
	Recv() ([]byte, error)
	// Close releases the stream, cancelling synthesis if it is still
	// running. It is safe to call more than once and after Recv failed.
	Close() error
}

// StreamSynthesizer is a synthesizer that can deliver audio while it is
// still being generated, so playback can start sooner.
type StreamSynthesizer interface {
	SynthesizeStream(ctx context.Context, text, lang string) (AudioStream, error)
}

// OpenStream starts synthesis of text on s: as a real stream if s is a
// StreamSynthesizer, otherwise as a one-chunk stream of the unary result.
func OpenStream(ctx context.Context, s Synthesizer, text, lang string) (AudioStream, error) {
	if ss, ok := s.(StreamSynthesizer); ok {
		return ss.SynthesizeStream(ctx, text, lang)
	}
	audio, err := s.Synthesize(ctx, text, lang)
	if err != nil {
		return nil, err
	}
	return SingleChunk(audio), nil
}

// SingleChunk returns a stream that yields audio.Data as one chunk.
func SingleChunk(audio Audio) AudioStream {
	return &singleChunk{audio: audio}
}

type singleChunk struct {
	mu    sync.Mutex
	audio Audio
	done  bool
}

func (s *singleChunk) Format() Format { return s.audio.Format }

func (s *singleChunk) Recv() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || len(s.audio.Data) == 0 {
		return nil, io.EOF
	}
	s.done = true
	return s.audio.Data, nil
}

func (s *singleChunk) Close() error { return nil }

// ReadAll drains and closes stream, returning the whole audio.
func ReadAll(stream AudioStream) (Audio, error) {
	defer stream.Close()
	audio := Audio{Format: stream.Format()}
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return audio, nil
		}
		if err != nil {
			return Audio{}, err
		}
		audio.Data = append(audio.Data, chunk...)
	}
}

// logTiming writes the per-utterance latency line. Its shape is stable so
// `kubectl logs | grep 'tts: timing'` can compare the paths:
//
//	tts: timing path=stream first_audio=412ms total=2.31s bytes=9123
//
// path is "rest" (unary), "stream" (gRPC streaming) or "rest-fallback" (the
// stream failed before any audio and the unary call was used; its times
// include the failed attempt). first_audio is from the request until the
// first audio byte; total until the last.
//
// The timing is also kept for the dashboard (RecentTimings).
func logTiming(path string, firstAudio, total time.Duration, bytes int) {
	recordTiming(Timing{At: time.Now(), Path: path, FirstAudio: firstAudio, Total: total, Bytes: bytes})
	log.Printf("tts: timing path=%s first_audio=%v total=%v bytes=%d",
		path, firstAudio.Round(time.Millisecond), total.Round(time.Millisecond), bytes)
}
