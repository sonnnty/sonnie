package voice

import (
	"context"
	"fmt"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// synthTimeout bounds a single synthesis + encode step so one slow engine
// call cannot stall the queue forever.
const synthTimeout = 30 * time.Second

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
	return p.queue.Push(text)
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
			text, ok := p.queue.Pop()
			if !ok {
				break
			}
			if err := p.speak(ctx, text); err != nil {
				if ctx.Err() != nil {
					return
				}
				if p.cfg.OnError != nil {
					p.cfg.OnError(err)
				}
			}
		}
	}
}

func (p *Player) speak(ctx context.Context, text string) error {
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
