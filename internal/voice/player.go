package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// synthTimeout bounds a single synthesis + encode step so one slow engine
// call cannot stall the queue forever.
const synthTimeout = 30 * time.Second

// streamIdleTimeout bounds each wait for audio on the streaming path (the
// first chunk included). Unlike synthTimeout it does not cover playback, which
// overlaps the stream there. A variable so tests can shorten it.
var streamIdleTimeout = synthTimeout

// streamBacklog is how many decoded chunks may wait for the sender. Opus
// speech is a few KB per second and an utterance is capped at a few hundred
// characters, so the receiver practically never waits on it.
const streamBacklog = 256

// PlayerConfig holds what a Player needs.
type PlayerConfig struct {
	Synth     tts.Synthesizer
	Encoder   Encoder
	Conn      Conn
	Lang      string
	QueueSize int
	// OnError is called for each utterance that fails. It may be nil.
	OnError func(error)
}

// Player speaks queued text into one voice connection, one utterance at a
// time and in order. Create it with NewPlayer and stop it with Close.
type Player struct {
	cfg    PlayerConfig
	queue  *queue
	cancel context.CancelFunc
	done   chan struct{}
}

// NewPlayer starts a Player. It does not own cfg.Conn: Close stops playback
// but does not disconnect.
func NewPlayer(cfg PlayerConfig) *Player {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Player{
		cfg:    cfg,
		queue:  newQueue(cfg.QueueSize),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go p.run(ctx)
	return p
}

// Enqueue adds text to the queue and reports whether the oldest waiting
// item was dropped to make room.
func (p *Player) Enqueue(text string) (dropped bool) {
	return p.EnqueueWith(text, nil)
}

// EnqueueWith is Enqueue with a hook that reports what became of the item.
// done(true) is called once all of its audio has been sent; done(false) when
// it will never be spoken: it was dropped from a full queue, or synthesis or
// playback failed. It is not called when Close discards the item. done runs
// on the player's goroutine (or the caller's, for a drop), so it must not
// block. The player knows nothing about where the text came from.
func (p *Player) EnqueueWith(text string, done func(spoken bool)) (dropped bool) {
	old, dropped := p.queue.Push(item{text: text, done: done})
	if dropped && old.done != nil {
		old.done(false)
	}
	return dropped
}

// Pending returns how many utterances are waiting (not counting the one
// being spoken).
func (p *Player) Pending() int {
	return p.queue.Len()
}

// Close stops playback, discards the queue and waits for the player to exit.
func (p *Player) Close() {
	p.cancel()
	<-p.done
}

func (p *Player) run(ctx context.Context) {
	defer close(p.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.queue.ready:
		}
		for {
			it, ok := p.queue.Pop()
			if !ok {
				break
			}
			err := p.speak(ctx, it.text)
			if err != nil && ctx.Err() != nil {
				return
			}
			if err != nil && p.cfg.OnError != nil {
				p.cfg.OnError(err)
			}
			if it.done != nil {
				it.done(err == nil)
			}
		}
	}
}

func (p *Player) speak(ctx context.Context, text string) error {
	if enc, ok := p.cfg.Encoder.(StreamEncoder); ok {
		return p.speakStream(ctx, enc, text)
	}
	return p.speakWhole(ctx, text)
}

// speakWhole synthesizes and encodes the whole utterance, then plays it.
func (p *Player) speakWhole(ctx context.Context, text string) error {
	prepCtx, cancel := context.WithTimeout(ctx, synthTimeout)
	defer cancel()

	audio, err := p.cfg.Synth.Synthesize(prepCtx, text, p.cfg.Lang)
	if err != nil {
		return fmt.Errorf("synthesize: %w", err)
	}
	frames, err := p.cfg.Encoder.Encode(prepCtx, audio)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if len(frames) == 0 {
		return nil
	}

	if err := p.cfg.Conn.Speaking(true); err != nil {
		return fmt.Errorf("speaking on: %w", err)
	}
	defer p.cfg.Conn.Speaking(false)

	for _, frame := range frames {
		if err := p.cfg.Conn.SendOpus(ctx, frame); err != nil {
			return fmt.Errorf("send: %w", err)
		}
	}
	return nil
}

// speakStream plays an utterance while it is still being synthesized: a
// receiver goroutine reads audio chunks and decodes them into frames as they
// arrive, and this goroutine sends them at the connection's 20 ms pace from
// the first frame on. Returning (done, Close, send failure) cancels the
// synthesis.
func (p *Player) speakStream(ctx context.Context, enc StreamEncoder, text string) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	idle := time.AfterFunc(streamIdleTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer idle.Stop()

	stream, err := tts.OpenStream(streamCtx, p.cfg.Synth, text, p.cfg.Lang)
	if err != nil {
		return fmt.Errorf("synthesize: %w", idleErr(&timedOut, err))
	}
	defer stream.Close()
	dec, err := enc.NewStream(stream.Format())
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	batches := make(chan [][]byte, streamBacklog)
	recvDone := make(chan error, 1)
	go func() {
		defer close(batches)
		recvDone <- receiveFrames(streamCtx, stream, dec, idle, batches)
	}()

	var sendErr error
	speaking := false
	for batch := range batches {
		if sendErr != nil {
			continue // drain until the receiver sees the cancellation
		}
		if !speaking {
			if err := p.cfg.Conn.Speaking(true); err != nil {
				sendErr = fmt.Errorf("speaking on: %w", err)
				cancel()
				continue
			}
			speaking = true
			defer p.cfg.Conn.Speaking(false)
		}
		for _, frame := range batch {
			if err := p.cfg.Conn.SendOpus(ctx, frame); err != nil {
				sendErr = fmt.Errorf("send: %w", err)
				cancel()
				break
			}
		}
	}
	recvErr := <-recvDone
	if sendErr != nil {
		return sendErr
	}
	if recvErr != nil {
		return idleErr(&timedOut, recvErr)
	}
	return nil
}

// receiveFrames reads stream to the end, feeding dec and passing on the
// frames of each chunk. The idle timer only runs while waiting for audio.
func receiveFrames(ctx context.Context, stream tts.AudioStream, dec FrameStream, idle *time.Timer, out chan<- [][]byte) error {
	for {
		idle.Reset(streamIdleTimeout)
		chunk, err := stream.Recv()
		idle.Stop()
		if errors.Is(err, io.EOF) {
			if err := dec.Close(); err != nil {
				return fmt.Errorf("encode: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("synthesize: %w", err)
		}
		frames, decErr := dec.Write(chunk)
		if len(frames) > 0 {
			select {
			case out <- frames:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if decErr != nil {
			return fmt.Errorf("encode: %w", decErr)
		}
	}
}

// idleErr names the idle timeout when it, rather than the caller, cancelled
// the stream.
func idleErr(timedOut *atomic.Bool, err error) error {
	if timedOut.Load() {
		return fmt.Errorf("no audio for %v: %w", streamIdleTimeout, err)
	}
	return err
}
