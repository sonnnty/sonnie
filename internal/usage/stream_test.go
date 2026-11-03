package usage

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// chunkStream yields chunks, then err (io.EOF if nil).
type chunkStream struct {
	format tts.Format
	chunks [][]byte
	err    error

	mu     sync.Mutex
	closed bool
}

func (s *chunkStream) Format() tts.Format { return s.format }

func (s *chunkStream) Recv() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.chunks) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return c, nil
}

func (s *chunkStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

// fakeStreamer streams the pages of an Ogg/Opus file one chunk per page,
// optionally cut after keep pages with err.
type fakeStreamer struct {
	audio   tts.Audio
	keep    int // pages to deliver; 0 = all
	err     error
	openErr error

	mu    sync.Mutex
	calls int
	last  *chunkStream
}

func (f *fakeStreamer) Synthesize(context.Context, string, string) (tts.Audio, error) {
	return tts.Audio{}, errors.New("fakeStreamer: unary path used")
}

func (f *fakeStreamer) SynthesizeStream(context.Context, string, string) (tts.AudioStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.openErr != nil {
		return nil, f.openErr
	}
	pages := splitOggPages(f.audio.Data)
	if f.keep > 0 {
		pages = pages[:f.keep]
	}
	f.last = &chunkStream{format: f.audio.Format, chunks: pages, err: f.err}
	return f.last, nil
}

func (f *fakeStreamer) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func splitOggPages(data []byte) [][]byte {
	var pages [][]byte
	for len(data) > 0 {
		n := 27 + int(data[26])
		for _, l := range data[27:n] {
			n += int(l)
		}
		pages = append(pages, data[:n])
		data = data[n:]
	}
	return pages
}

// oggOpusFrames is a stream of n pages of one 20 ms frame each after the
// two header pages, with pre-skip 312.
func oggOpusFrames(n int) tts.Audio {
	a := oggOpus(0)
	data := a.Data[:len(splitOggPages(a.Data)[0])+len(splitOggPages(a.Data)[1])]
	for i := 1; i <= n; i++ {
		data = append(data, oggPage(312+int64(i)*960, []byte{0x0b, byte(i)})...)
	}
	return tts.Audio{Format: tts.FormatOggOpus, Data: data}
}

func drain(t *testing.T, s tts.AudioStream) error {
	t.Helper()
	for {
		_, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var input = strings.Repeat("ก", 100) // 100 + 20 prompt runes = 120 input tokens

func TestMeterStream_ChargesLikeUnary(t *testing.T) {
	audio := oggOpus(4 * time.Second)
	m := moneyMeter(&fakeStreamer{audio: audio}, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	if err := drain(t, s); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.Close()
	// Same numbers as TestMeterMoney_ChargesSuccessfulRequest.
	if snap := m.Snapshot(); snap.Used != 34980 || snap.AudioMs != 4000 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestMeterStream_PartialStreamEndingInErrorIsChargedForWhatArrived(t *testing.T) {
	// 50 frames (1 s) would be generated; 10 arrive (2 headers + 10 audio
	// pages) before the stream breaks.
	inner := &fakeStreamer{audio: oggOpusFrames(50), keep: 12, err: errors.New("rpc reset")}
	m := moneyMeter(inner, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	if err := drain(t, s); err == nil {
		t.Fatal("want the stream error")
	}
	s.Close()
	want := CostMicroTHB(200*time.Millisecond, 120, 33)
	if snap := m.Snapshot(); snap.Used != want || snap.AudioMs != 200 {
		t.Fatalf("snapshot %+v, want %d µ฿ for 200 ms", snap, want)
	}
}

func TestMeterStream_CloseMidStreamChargesWhatWasRead(t *testing.T) {
	inner := &fakeStreamer{audio: oggOpusFrames(50)}
	m := moneyMeter(inner, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 + 25 { // headers + 25 frames = 500 ms
		if _, err := s.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if snap := m.Snapshot(); snap.AudioMs != 500 {
		t.Fatalf("snapshot %+v, want 500 ms", snap)
	}
	if !inner.last.closed {
		t.Fatal("inner stream not closed")
	}
	// Reading on after Close charges nothing more.
	_ = drain(t, s)
	if snap := m.Snapshot(); snap.AudioMs != 500 {
		t.Fatalf("charged twice: %+v", snap)
	}
}

func TestMeterStream_NoAudioIsNotCharged(t *testing.T) {
	inner := &fakeStreamer{audio: tts.Audio{Format: tts.FormatOggOpus}, err: errors.New("failed before audio")}
	m := moneyMeter(inner, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	_ = drain(t, s)
	s.Close()
	if snap := m.Snapshot(); snap.Used != 0 {
		t.Fatalf("snapshot %+v", snap)
	}

	failing := &fakeStreamer{openErr: errors.New("open failed")}
	m = moneyMeter(failing, 300, 33, nil)
	if _, err := m.SynthesizeStream(context.Background(), input, "th"); err == nil {
		t.Fatal("want the open error")
	}
	if snap := m.Snapshot(); snap.Used != 0 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestMeterStream_BudgetIsRefusedBeforeTheRequest(t *testing.T) {
	inner := &fakeStreamer{audio: oggOpus(time.Hour)}
	m := moneyMeter(inner, 1, 33, nil) // one hour of audio is far over ฿1
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	_ = drain(t, s)
	s.Close()

	_, err = m.SynthesizeStream(context.Background(), input, "th")
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("want ErrLimitReached, got %v", err)
	}
	if inner.Calls() != 1 {
		t.Fatalf("inner called %d times, want 1", inner.Calls())
	}
	if !m.Snapshot().Paused {
		t.Fatal("meter not paused")
	}
}

func TestMeterStream_MalformedAudioIsBilledByUpperBound(t *testing.T) {
	m := moneyMeter(&streamFunc{chunks: [][]byte{make([]byte, 6000)}}, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	_ = drain(t, s)
	s.Close()
	// 6000 bytes at 6 kbit/s = 8 s.
	if snap := m.Snapshot(); snap.AudioMs != 8000 {
		t.Fatalf("snapshot %+v", snap)
	}
}

// streamFunc streams fixed chunks claiming to be Ogg/Opus.
type streamFunc struct{ chunks [][]byte }

func (f *streamFunc) Synthesize(context.Context, string, string) (tts.Audio, error) {
	return tts.Audio{}, errors.New("unused")
}

func (f *streamFunc) SynthesizeStream(context.Context, string, string) (tts.AudioStream, error) {
	return &chunkStream{format: tts.FormatOggOpus, chunks: f.chunks}, nil
}

func TestMeterStream_UnaryInnerIsChargedWhole(t *testing.T) {
	m := moneyMeter(&fakeGemini{dur: 4 * time.Second}, 300, 33, nil)
	s, err := m.SynthesizeStream(context.Background(), input, "th")
	if err != nil {
		t.Fatal(err)
	}
	if err := drain(t, s); err != nil {
		t.Fatal(err)
	}
	if snap := m.Snapshot(); snap.Used != 34980 || snap.AudioMs != 4000 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestMeterStream_CharsModeCountsBeforeTheRequest(t *testing.T) {
	m := New(Config{Inner: &tts.Fake{}, Limit: 10, Now: newClock().Now})
	s, err := m.SynthesizeStream(context.Background(), text(6), "th")
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // nothing read: characters are still counted
	if snap := m.Snapshot(); snap.Used != 6 {
		t.Fatalf("snapshot %+v", snap)
	}
	if _, err := m.SynthesizeStream(context.Background(), text(6), "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("want ErrLimitReached, got %v", err)
	}
}

func TestMeterStream_CancelledContext(t *testing.T) {
	inner := &fakeStreamer{audio: oggOpus(time.Second)}
	m := moneyMeter(inner, 300, 33, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.SynthesizeStream(ctx, "x", "th"); err == nil || inner.Calls() != 0 {
		t.Fatalf("err %v, calls %d", err, inner.Calls())
	}
}
