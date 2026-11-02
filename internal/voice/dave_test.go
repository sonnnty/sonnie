package voice

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	dvoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
)

// fakeVoiceConn stands in for a disgo voice connection. Embedding the
// interfaces satisfies them; methods daveConn should not call panic.
type fakeVoiceConn struct {
	dvoice.Conn
	udp  *fakeUDP
	dave *fakeDAVE

	mu       sync.Mutex
	speaking []dvoice.SpeakingFlags
	closes   int
}

func newFakeVoiceConn(ready bool) *fakeVoiceConn {
	c := &fakeVoiceConn{udp: &fakeUDP{}, dave: &fakeDAVE{}}
	c.dave.ready.Store(ready)
	return c
}

func (c *fakeVoiceConn) UDP() dvoice.UDPConn  { return c.udp }
func (c *fakeVoiceConn) DAVE() godave.Session { return c.dave }

func (c *fakeVoiceConn) SetSpeaking(_ context.Context, flags dvoice.SpeakingFlags) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speaking = append(c.speaking, flags)
	return nil
}

func (c *fakeVoiceConn) Close(context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
}

type fakeUDP struct {
	dvoice.UDPConn

	mu     sync.Mutex
	frames [][]byte
	times  []time.Time
}

func (u *fakeUDP) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.frames = append(u.frames, append([]byte(nil), p...))
	u.times = append(u.times, time.Now())
	return len(p), nil
}

func (u *fakeUDP) written() ([][]byte, []time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([][]byte(nil), u.frames...), append([]time.Time(nil), u.times...)
}

type fakeDAVE struct {
	godave.Session
	ready atomic.Bool
}

func (d *fakeDAVE) Ready() bool { return d.ready.Load() }

func TestVoiceStateEvent(t *testing.T) {
	ev, ok := voiceStateEvent(&discordgo.VoiceState{
		GuildID: "100", ChannelID: "200", UserID: "300",
		SessionID: "sess", SelfDeaf: true,
	})
	if !ok {
		t.Fatal("expected conversion to succeed")
	}
	if ev.GuildID != 100 || ev.UserID != 300 || ev.SessionID != "sess" || !ev.SelfDeaf || ev.SelfMute {
		t.Fatalf("unexpected event %+v", ev)
	}
	if ev.ChannelID == nil || *ev.ChannelID != 200 {
		t.Fatalf("expected channel 200, got %v", ev.ChannelID)
	}

	left, ok := voiceStateEvent(&discordgo.VoiceState{GuildID: "100", UserID: "300"})
	if !ok || left.ChannelID != nil {
		t.Fatalf("expected a leave event with nil channel, got %+v (ok=%v)", left, ok)
	}

	for _, vs := range []*discordgo.VoiceState{
		{GuildID: "x", UserID: "300"},
		{GuildID: "100", UserID: ""},
		{GuildID: "100", UserID: "300", ChannelID: "y"},
	} {
		if _, ok := voiceStateEvent(vs); ok {
			t.Fatalf("expected %+v to be rejected", vs)
		}
	}
}

func TestVoiceServerEvent(t *testing.T) {
	ev, ok := voiceServerEvent(&discordgo.VoiceServerUpdate{GuildID: "100", Token: "tok", Endpoint: "x.discord.media"})
	if !ok || ev.GuildID != 100 || ev.Token != "tok" || ev.Endpoint == nil || *ev.Endpoint != "x.discord.media" {
		t.Fatalf("unexpected event %+v (ok=%v)", ev, ok)
	}

	gone, ok := voiceServerEvent(&discordgo.VoiceServerUpdate{GuildID: "100", Token: "tok"})
	if !ok || gone.Endpoint != nil {
		t.Fatalf("expected nil endpoint, got %+v (ok=%v)", gone, ok)
	}

	if _, ok := voiceServerEvent(&discordgo.VoiceServerUpdate{GuildID: "x"}); ok {
		t.Fatal("expected a bad guild ID to be rejected")
	}
}

func TestDAVEConn_PacesFrames(t *testing.T) {
	vc := newFakeVoiceConn(true)
	c := newDAVEConn(vc)

	const n = 5
	for i := range n {
		if err := c.SendOpus(context.Background(), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}

	frames, times := vc.udp.written()
	if len(frames) != n {
		t.Fatalf("expected %d frames, got %d", n, len(frames))
	}
	for i, f := range frames {
		if !bytes.Equal(f, []byte{byte(i)}) {
			t.Fatalf("frame %d out of order: %v", i, f)
		}
	}
	// Timers never fire early, so the lower bound is exact; allow 1 ms for
	// clock granularity.
	if got, min := times[n-1].Sub(times[0]), (n-1)*frameDuration-time.Millisecond; got < min {
		t.Fatalf("frames sent too fast: %v for %d frames", got, n)
	}
}

func TestDAVEConn_WaitsForDAVE(t *testing.T) {
	vc := newFakeVoiceConn(false)
	c := newDAVEConn(vc)

	done := make(chan error, 1)
	go func() { done <- c.SendOpus(context.Background(), []byte("hi")) }()

	select {
	case err := <-done:
		t.Fatalf("SendOpus returned before DAVE was ready: %v", err)
	case <-time.After(5 * frameDuration):
	}
	if frames, _ := vc.udp.written(); len(frames) != 0 {
		t.Fatalf("expected no frames before DAVE is ready, got %d", len(frames))
	}

	vc.dave.ready.Store(true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendOpus did not resume after DAVE became ready")
	}
	if frames, _ := vc.udp.written(); len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
}

func TestDAVEConn_DAVENeverReady(t *testing.T) {
	vc := newFakeVoiceConn(false)
	c := newDAVEConn(vc)
	c.readyTimeout = 3 * frameDuration

	if err := c.SendOpus(context.Background(), []byte("hi")); !errors.Is(err, ErrE2EENotReady) {
		t.Fatalf("expected ErrE2EENotReady, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.readyTimeout = time.Minute
	if err := c.SendOpus(ctx, []byte("hi")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if frames, _ := vc.udp.written(); len(frames) != 0 {
		t.Fatalf("expected no frames, got %d", len(frames))
	}
}

func TestDAVEConn_SpeakingEndsWithSilence(t *testing.T) {
	vc := newFakeVoiceConn(true)
	c := newDAVEConn(vc)

	if err := c.Speaking(true); err != nil {
		t.Fatal(err)
	}
	if err := c.SendOpus(context.Background(), []byte("audio")); err != nil {
		t.Fatal(err)
	}
	if err := c.Speaking(false); err != nil {
		t.Fatal(err)
	}

	frames, _ := vc.udp.written()
	if len(frames) != 1+silenceFrames {
		t.Fatalf("expected audio plus %d silence frames, got %d frames", silenceFrames, len(frames))
	}
	for _, f := range frames[1:] {
		if !bytes.Equal(f, dvoice.SilenceAudioFrame) {
			t.Fatalf("expected a silence frame, got %v", f)
		}
	}
	want := []dvoice.SpeakingFlags{dvoice.SpeakingFlagMicrophone, dvoice.SpeakingFlagNone}
	if len(vc.speaking) != len(want) || vc.speaking[0] != want[0] || vc.speaking[1] != want[1] {
		t.Fatalf("expected speaking flags %v, got %v", want, vc.speaking)
	}

	// No audio since the last stop: no more silence.
	if err := c.Speaking(false); err != nil {
		t.Fatal(err)
	}
	if frames, _ := vc.udp.written(); len(frames) != 1+silenceFrames {
		t.Fatalf("expected no extra silence, got %d frames", len(frames))
	}
}

func TestDAVEConn_Disconnect(t *testing.T) {
	vc := newFakeVoiceConn(false)
	c := newDAVEConn(vc)

	// A send stuck waiting for DAVE is released by Disconnect.
	done := make(chan error, 1)
	go func() { done <- c.SendOpus(context.Background(), []byte("hi")) }()
	time.Sleep(2 * frameDuration)

	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("expected ErrConnClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendOpus was not released by Disconnect")
	}

	vc.dave.ready.Store(true)
	if err := c.SendOpus(context.Background(), []byte("hi")); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("expected ErrConnClosed after disconnect, got %v", err)
	}
	if vc.closes != 1 {
		t.Fatalf("expected the voice conn to be closed once, got %d", vc.closes)
	}
}

func TestDAVEJoiner_JoinBeforeConnected(t *testing.T) {
	s, err := discordgo.New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	j := NewDAVEJoiner(s)

	if _, err := j.Join(context.Background(), "100", "200"); !errors.Is(err, errNotConnected) {
		t.Fatalf("expected errNotConnected, got %v", err)
	}
	if _, err := j.Join(context.Background(), "guild", "200"); err == nil {
		t.Fatal("expected a bad guild ID to fail")
	}
	// Events before the first Join have no manager to go to and are dropped.
	j.onVoiceServerUpdate(s, &discordgo.VoiceServerUpdate{GuildID: "100", Token: "tok", Endpoint: "x"})
}
