package tts

import (
	"context"
	"sync"
)

// Request is one call recorded by Fake.
type Request struct {
	Text string
	Lang string
}

// Fake is an in-memory Synthesizer for tests. It records every request and
// returns the text itself as FormatRaw audio. It is safe for concurrent use.
type Fake struct {
	// Err, if set, is returned instead of audio.
	Err error

	mu       sync.Mutex
	requests []Request
}

func (f *Fake) Synthesize(ctx context.Context, text, lang string) (Audio, error) {
	if err := ctx.Err(); err != nil {
		return Audio{}, err
	}
	f.mu.Lock()
	f.requests = append(f.requests, Request{Text: text, Lang: lang})
	f.mu.Unlock()
	if f.Err != nil {
		return Audio{}, f.Err
	}
	return Audio{Format: FormatRaw, Data: []byte(text)}, nil
}

// Requests returns a copy of the requests received so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}
