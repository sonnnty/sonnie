package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// chanStream is an AudioStream fed by the test through chunks; closing
// chunks ends it with err (io.EOF if nil).
type chanStream struct {
	ctx    context.Context
	format tts.Format
	chunks chan []byte
	err    error

	closeOnce sync.Once
	closed    chan struct{}
}

func newChanStream(ctx context.Context, format tts.Format) *chanStream {
	return &chanStream{ctx: ctx, format: format, chunks: make(chan []byte, 64), closed: make(chan struct{})}
}

func (s *chanStream) Format() tts.Format { return s.format }

func (s *chanStream) Recv() ([]byte, error) {
	select {
	case c, ok := <-s.chunks:
		if ok {
			return c, nil
		}
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *chanStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

// streamSynth hands each SynthesizeStream call a new chanStream on opened.
type streamSynth struct {
	format tts.Format
	opened chan *chanStream
	err    error
}

func newStreamSynth() *streamSynth {
	return &streamSynth{format: tts.FormatRaw, opened: make(chan *chanStream, 16)}
}

func (s *streamSynth) Synthesize(context.Context, string, string) (tts.Audio, error) {
	return tts.Audio{}, errors.New("streamSynth: unary path used")
}

func (s *streamSynth) SynthesizeStream(ctx context.Context, _, _ string) (tts.AudioStream, error) {
	if s.err != nil {
		return nil, s.err
	}
	st := newChanStream(ctx, s.format)
	s.opened <- st
	return st, nil
}

func waitStream(t *testing.T, s *streamSynth) *chanStream {
	t.Helper()
	select {
	case st := <-s.opened:
		return st
	case <-time.After(2 * time.Second):
		t.Fatal("no stream was opened")
		return nil
	}
}

func waitClosed(t *testing.T, st *chanStream) {
	t.Helper()
	select {
	case <-st.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream was not closed")
	}
	if st.ctx.Err() == nil {
		t.Fatal("stream context was not cancelled")
	}
}

func TestPlayerStream_StartsSendingBeforeTheStreamEnds(t *testing.T) {
	synth := newStreamSynth()
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: conn, QueueSize: 10})
	defer p.Close()

	p.Enqueue("first")
	p.Enqueue("second")
	st := waitStream(t, synth)
	st.chunks <- []byte("a")
	if got := waitFrame(t, conn.Sent); got != "a" {
		t.Fatalf("got %q", got)
	}
	// The stream is still open: more audio follows.
	st.chunks <- []byte("b")
	if got := waitFrame(t, conn.Sent); got != "b" {
		t.Fatalf("got %q", got)
	}
	select {
	case <-synth.opened:
		t.Fatal("next utterance started while the first was still streaming")
	default:
	}
	close(st.chunks)
	waitClosed(t, st)

	st2 := waitStream(t, synth)
	st2.chunks <- []byte("c")
	close(st2.chunks)
	if got := waitFrame(t, conn.Sent); got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestPlayerStream_CloseCancelsAStreamWaitingForAudio(t *testing.T) {
	synth := newStreamSynth()
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: &FakeConn{}, QueueSize: 10})
	p.Enqueue("x")
	st := waitStream(t, synth)

	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
	waitClosed(t, st)
}

func TestPlayerStream_CloseWhileSendingCancelsTheStream(t *testing.T) {
	synth := newStreamSynth()
	// Nobody reads Sent, so the first SendOpus blocks until Close.
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: conn, QueueSize: 10})
	p.Enqueue("x")
	st := waitStream(t, synth)
	st.chunks <- []byte("a")

	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
	waitClosed(t, st)
	if conn.Disconnected() {
		t.Fatal("Player must not disconnect the connection it does not own")
	}
}

func TestPlayerStream_ErrorMidStreamIsReportedAndQueueContinues(t *testing.T) {
	synth := newStreamSynth()
	conn := &FakeConn{Sent: make(chan []byte, 16)}
	errs := make(chan error, 4)
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: conn, QueueSize: 10,
		OnError: func(err error) { errs <- err }})
	defer p.Close()

	p.Enqueue("broken")
	p.Enqueue("fine")
	st := waitStream(t, synth)
	st.chunks <- []byte("a")
	st.err = errors.New("rpc reset")
	close(st.chunks)

	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "rpc reset") {
			t.Fatalf("error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error reported")
	}
	if got := waitFrame(t, conn.Sent); got != "a" {
		t.Fatalf("audio before the error was not played: %q", got)
	}
	st2 := waitStream(t, synth)
	st2.chunks <- []byte("ok")
	close(st2.chunks)
	if got := waitFrame(t, conn.Sent); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestPlayerStream_IdleTimeout(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = old })

	synth := newStreamSynth()
	errs := make(chan error, 1)
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: &FakeConn{}, QueueSize: 10,
		OnError: func(err error) { errs <- err }})
	defer p.Close()
	p.Enqueue("x")
	st := waitStream(t, synth)
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "no audio for") {
			t.Fatalf("error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled stream was not timed out")
	}
	waitClosed(t, st)
}

func TestPlayerStream_SynthesisErrorIsReported(t *testing.T) {
	synth := newStreamSynth()
	synth.err = tts.ErrNotImplemented
	errs := make(chan error, 1)
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: FakeStreamEncoder{}, Conn: &FakeConn{}, QueueSize: 10,
		OnError: func(err error) { errs <- err }})
	defer p.Close()
	p.Enqueue("x")
	select {
	case err := <-errs:
		if !errors.Is(err, tts.ErrNotImplemented) {
			t.Fatalf("error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error reported")
	}
}

// A unary synthesizer still plays through a StreamEncoder (as one chunk).
func TestPlayerStream_UnarySynthesizer(t *testing.T) {
	conn := &FakeConn{Sent: make(chan []byte)}
	p := NewPlayer(PlayerConfig{Synth: &tts.Fake{}, Encoder: FakeStreamEncoder{}, Conn: conn, QueueSize: 10})
	defer p.Close()
	p.Enqueue("hello")
	if got := waitFrame(t, conn.Sent); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

// Real Ogg pages through the real demuxer: the frames sent equal the
// whole-file result, and the first goes out before the last page arrives.
func TestPlayerStream_OggFixturePageByPage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "google-th-24k.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{Format: tts.FormatOggOpus, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	synth := newStreamSynth()
	synth.format = tts.FormatOggOpus
	conn := &FakeConn{Sent: make(chan []byte, len(want))}
	p := NewPlayer(PlayerConfig{Synth: synth, Encoder: OggOpusEncoder{}, Conn: conn, QueueSize: 10})
	defer p.Close()
	p.Enqueue("x")
	st := waitStream(t, synth)

	pages := splitPages(t, data)
	for _, pg := range pages[:3] { // headers and the first audio page
		st.chunks <- pg
	}
	if got := waitFrame(t, conn.Sent); got != string(want[0]) {
		t.Fatal("first frame differs")
	}
	for _, pg := range pages[3:] {
		st.chunks <- pg
	}
	close(st.chunks)
	for i := 1; i < len(want); i++ {
		if got := waitFrame(t, conn.Sent); !bytes.Equal([]byte(got), want[i]) {
			t.Fatalf("frame %d differs", i)
		}
	}
}
