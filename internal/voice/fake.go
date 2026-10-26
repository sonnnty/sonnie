package voice

import (
	"context"
	"sync"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// FakeEncoder is an Encoder for tests: it returns the audio bytes as a
// single frame.
type FakeEncoder struct{}

func (FakeEncoder) Encode(_ context.Context, audio tts.Audio) ([][]byte, error) {
	return [][]byte{audio.Data}, nil
}

// FakeConn is an in-memory Conn for tests. It records frames and whether it
// was disconnected. It is safe for concurrent use.
type FakeConn struct {
	// Sent, if non-nil, receives a copy of every frame after it is recorded,
	// letting tests wait for playback without sleeping.
	Sent chan []byte

	mu           sync.Mutex
	frames       [][]byte
	disconnected bool
}

func (c *FakeConn) Speaking(bool) error { return nil }

func (c *FakeConn) SendOpus(ctx context.Context, frame []byte) error {
	c.mu.Lock()
	c.frames = append(c.frames, frame)
	c.mu.Unlock()
	if c.Sent != nil {
		select {
		case c.Sent <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *FakeConn) Disconnect() error {
	c.mu.Lock()
	c.disconnected = true
	c.mu.Unlock()
	return nil
}

// Frames returns a copy of the frames sent so far.
func (c *FakeConn) Frames() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.frames...)
}

// Disconnected reports whether Disconnect was called.
func (c *FakeConn) Disconnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnected
}
