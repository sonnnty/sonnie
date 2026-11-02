package readaloud

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/voice"
)

type fakeJoiner struct {
	mu       sync.Mutex
	err      error
	joins    []string // "guild/channel"
	conns    []*voice.FakeConn
	presence *fakePresence
	onJoin   func(guildID, channelID string)
}

func (j *fakeJoiner) Join(_ context.Context, guildID, channelID string) (voice.Conn, error) {
	j.mu.Lock()
	if j.err != nil {
		j.mu.Unlock()
		return nil, j.err
	}
	j.joins = append(j.joins, guildID+"/"+channelID)
	c := &voice.FakeConn{Sent: make(chan []byte, 16)}
	j.conns = append(j.conns, c)
	p := j.presence
	onJoin := j.onJoin
	j.mu.Unlock()

	if p != nil {
		p.setBotChannel(channelID)
	}
	if onJoin != nil {
		onJoin(guildID, channelID)
	}
	return c, nil
}

func (j *fakeJoiner) conn(i int) *voice.FakeConn {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.conns[i]
}

type fakePresence struct {
	mu         sync.Mutex
	humans     map[string]int // channel -> humans
	botChannel string
}

func (p *fakePresence) HumansInVoice(_, channelID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.humans[channelID]
}

func (p *fakePresence) BotVoiceChannel(string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.botChannel
}

func (p *fakePresence) setBotChannel(channelID string) {
	p.mu.Lock()
	p.botChannel = channelID
	p.mu.Unlock()
}

func (p *fakePresence) setHumans(channelID string, n int) {
	p.mu.Lock()
	p.humans[channelID] = n
	p.mu.Unlock()
}

func newTestManager(j *fakeJoiner, p Presence) *Manager {
	if fp, ok := p.(*fakePresence); ok && j != nil {
		j.presence = fp
	}
	return New(Config{
		Synth:       &tts.Fake{},
		Encoder:     voice.FakeEncoder{},
		Joiner:      j,
		Presence:    p,
		Lang:        "th",
		MaxChars:    200,
		QueueSize:   10,
		SpeakAuthor: true,
	})
}

func recv(t *testing.T, c *voice.FakeConn) string {
	t.Helper()
	select {
	case f := <-c.Sent:
		return string(f)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for speech")
		return ""
	}
}

func TestManager_JoinThenSpeaksBoundChannel(t *testing.T) {
	j := &fakeJoiner{}
	m := newTestManager(j, nil)
	defer m.Close()

	if err := m.Join(context.Background(), "g1", "v1", "t1"); err != nil {
		t.Fatalf("join: %v", err)
	}
	if text, vc, ok := m.Bound("g1"); !ok || text != "t1" || vc != "v1" {
		t.Fatalf("unexpected binding: %q %q %v", text, vc, ok)
	}

	if !m.HandleMessage(Message{GuildID: "g1", ChannelID: "t1", AuthorName: "Somchai", Content: "ดู https://x.y"}, nil) {
		t.Fatal("expected message to be queued")
	}
	if got, want := recv(t, j.conn(0)), "Somchai: ดู ลิงก์"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestManager_IgnoresOtherChannelsBotsAndEmpty(t *testing.T) {
	m := newTestManager(&fakeJoiner{}, nil)
	defer m.Close()
	if err := m.Join(context.Background(), "g1", "v1", "t1"); err != nil {
		t.Fatalf("join: %v", err)
	}

	tests := []struct {
		name string
		msg  Message
	}{
		{"other channel", Message{GuildID: "g1", ChannelID: "t2", Content: "hi"}},
		{"other guild", Message{GuildID: "g2", ChannelID: "t1", Content: "hi"}},
		{"bot", Message{GuildID: "g1", ChannelID: "t1", Content: "hi", IsBot: true}},
		{"empty", Message{GuildID: "g1", ChannelID: "t1", Content: ""}},
		{"empty after normalising", Message{GuildID: "g1", ChannelID: "t1", Content: "<:pepe:1>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if m.HandleMessage(tt.msg, nil) {
				t.Fatal("expected message to be ignored")
			}
		})
	}
}

func TestManager_RejoinSameVoiceChannelOnlyRebindsText(t *testing.T) {
	j := &fakeJoiner{}
	m := newTestManager(j, nil)
	defer m.Close()

	_ = m.Join(context.Background(), "g1", "v1", "t1")
	_ = m.Join(context.Background(), "g1", "v1", "t2")

	if len(j.joins) != 1 {
		t.Fatalf("expected one voice join, got %v", j.joins)
	}
	if text, _, _ := m.Bound("g1"); text != "t2" {
		t.Fatalf("expected text channel t2, got %q", text)
	}
}

func TestManager_JoinOtherVoiceChannelReplacesBinding(t *testing.T) {
	j := &fakeJoiner{}
	m := newTestManager(j, nil)
	defer m.Close()

	_ = m.Join(context.Background(), "g1", "v1", "t1")
	_ = m.Join(context.Background(), "g1", "v2", "t1")

	if len(j.joins) != 2 {
		t.Fatalf("expected two voice joins, got %v", j.joins)
	}
	if !j.conn(0).Disconnected() {
		t.Fatal("expected the old connection to be disconnected")
	}
	if _, vc, _ := m.Bound("g1"); vc != "v2" {
		t.Fatalf("expected voice channel v2, got %q", vc)
	}
}

func TestManager_JoinError(t *testing.T) {
	want := errors.New("no permission")
	m := newTestManager(&fakeJoiner{err: want}, nil)
	if err := m.Join(context.Background(), "g1", "v1", "t1"); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
	if _, _, ok := m.Bound("g1"); ok {
		t.Fatal("expected no binding after a failed join")
	}
}

func TestManager_Leave(t *testing.T) {
	j := &fakeJoiner{}
	m := newTestManager(j, nil)

	if err := m.Leave("g1"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("expected ErrNotBound, got %v", err)
	}
	_ = m.Join(context.Background(), "g1", "v1", "t1")
	if err := m.Leave("g1"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if !j.conn(0).Disconnected() {
		t.Fatal("expected disconnect on leave")
	}
	if m.HandleMessage(Message{GuildID: "g1", ChannelID: "t1", Content: "hi"}, nil) {
		t.Fatal("expected no speech after leave")
	}
}

func TestManager_AutoLeavesEmptyVoiceChannel(t *testing.T) {
	j := &fakeJoiner{}
	presence := &fakePresence{humans: map[string]int{"v1": 1}}
	m := newTestManager(j, presence)
	defer m.Close()
	_ = m.Join(context.Background(), "g1", "v1", "t1")

	m.HandleVoiceStateUpdate("g1")
	if _, _, ok := m.Bound("g1"); !ok {
		t.Fatal("left while a human was still in the channel")
	}

	presence.setHumans("v1", 0)
	m.HandleVoiceStateUpdate("g1")
	if _, _, ok := m.Bound("g1"); ok {
		t.Fatal("expected to leave an empty channel")
	}
	if !j.conn(0).Disconnected() {
		t.Fatal("expected disconnect on auto-leave")
	}
}

func TestManager_DropsBindingWhenBotDisconnected(t *testing.T) {
	j := &fakeJoiner{}
	presence := &fakePresence{humans: map[string]int{"v1": 1}}
	m := newTestManager(j, presence)
	defer m.Close()
	if err := m.Join(context.Background(), "g1", "v1", "t1"); err != nil {
		t.Fatalf("join: %v", err)
	}

	presence.setBotChannel("")
	m.HandleVoiceStateUpdate("g1")

	if _, _, ok := m.Bound("g1"); ok {
		t.Fatal("expected binding to be dropped when bot is disconnected")
	}
	if j.conn(0).Disconnected() {
		t.Fatal("expected not to disconnect again when already gone from voice")
	}
	if m.HandleMessage(Message{GuildID: "g1", ChannelID: "t1", Content: "hi"}, nil) {
		t.Fatal("expected no speech after binding is dropped")
	}
}

func TestManager_DropsBindingWhenBotMovedBySomeoneElse(t *testing.T) {
	j := &fakeJoiner{}
	presence := &fakePresence{humans: map[string]int{"v1": 1, "v2": 1}}
	m := newTestManager(j, presence)
	defer m.Close()
	if err := m.Join(context.Background(), "g1", "v1", "t1"); err != nil {
		t.Fatalf("join: %v", err)
	}

	presence.setBotChannel("v2")
	m.HandleVoiceStateUpdate("g1")

	if _, _, ok := m.Bound("g1"); ok {
		t.Fatal("expected binding to be dropped when bot is moved to another channel")
	}
	if !j.conn(0).Disconnected() {
		t.Fatal("expected bot to disconnect from the channel it was moved to")
	}
	if m.HandleMessage(Message{GuildID: "g1", ChannelID: "t1", Content: "hi"}, nil) {
		t.Fatal("expected no speech after binding is dropped")
	}
}

func TestManager_KeepsBindingWhenMoveCausedByJoin(t *testing.T) {
	j := &fakeJoiner{}
	presence := &fakePresence{humans: map[string]int{"v1": 1, "v2": 1}}
	m := newTestManager(j, presence)
	defer m.Close()
	if err := m.Join(context.Background(), "g1", "v1", "t1"); err != nil {
		t.Fatalf("first join: %v", err)
	}

	// Simulate Discord firing VoiceStateUpdate both while /tts join is in
	// flight and after it completes.
	j.onJoin = func(guildID, _ string) {
		m.HandleVoiceStateUpdate(guildID)
	}
	if err := m.Join(context.Background(), "g1", "v2", "t2"); err != nil {
		t.Fatalf("second join: %v", err)
	}
	m.HandleVoiceStateUpdate("g1")

	if text, vc, ok := m.Bound("g1"); !ok || text != "t2" || vc != "v2" {
		t.Fatalf("expected binding (t2, v2, true), got (%q, %q, %v)", text, vc, ok)
	}
	if !j.conn(0).Disconnected() {
		t.Fatal("expected old connection to be disconnected")
	}
	if j.conn(1).Disconnected() {
		t.Fatal("expected new connection to stay connected")
	}
	if !m.HandleMessage(Message{GuildID: "g1", ChannelID: "t2", AuthorName: "Somchai", Content: "สวัสดี"}, nil) {
		t.Fatal("expected message in new bound channel to be queued")
	}
	if got, want := recv(t, j.conn(1)), "Somchai: สวัสดี"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestManager_CloseLeavesAll(t *testing.T) {
	j := &fakeJoiner{}
	m := newTestManager(j, nil)
	_ = m.Join(context.Background(), "g1", "v1", "t1")
	_ = m.Join(context.Background(), "g2", "v2", "t2")

	m.Close()
	for i := 0; i < 2; i++ {
		if !j.conn(i).Disconnected() {
			t.Fatalf("connection %d not disconnected", i)
		}
	}
}
