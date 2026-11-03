// Package readaloud owns the "read chat aloud" state: which text channel is
// bound to which voice connection in each guild, and the player that speaks
// it. Discord-specific parts are behind the Joiner and Presence interfaces
// so the logic can be tested with fakes.
package readaloud

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/voice"
)

// ErrNotBound is returned by Leave when the guild has no active binding.
var ErrNotBound = errors.New("readaloud: not reading in this guild")

// Joiner connects to a voice channel.
type Joiner interface {
	Join(ctx context.Context, guildID, channelID string) (voice.Conn, error)
}

// Presence reports voice channel occupancy in a guild.
type Presence interface {
	// HumansInVoice counts the humans (non-bots) in channelID.
	HumansInVoice(guildID, channelID string) int
	// BotVoiceChannel returns the voice channel the bot is currently in, or
	// "" if it is not in a voice channel in guildID.
	BotVoiceChannel(guildID string) string
}

// Config holds the Manager's dependencies and settings.
type Config struct {
	Synth    tts.Synthesizer
	Encoder  voice.Encoder
	Joiner   Joiner
	Presence Presence

	Lang        string
	MaxChars    int
	QueueSize   int
	SpeakAuthor bool
}

type binding struct {
	textChannelID  string
	voiceChannelID string
	conn           voice.Conn
	player         *voice.Player
}

// Manager holds at most one binding per guild. It is safe for concurrent
// use.
type Manager struct {
	cfg      Config
	linkWord string

	// joinMu serialises Join and Leave so two concurrent /tts join calls
	// cannot both connect. It is held across the (slow) voice join, so
	// mu, which message handling takes, is kept separate.
	joinMu sync.Mutex

	mu       sync.Mutex
	bindings map[string]*binding

	// limitLogged is the month for which the monthly-limit error was last
	// logged, so it is logged once rather than for every skipped message.
	limitMu     sync.Mutex
	limitLogged string
}

// New creates a Manager.
func New(cfg Config) *Manager {
	return &Manager{
		cfg:      cfg,
		linkWord: DefaultLinkWord(cfg.Lang),
		bindings: make(map[string]*binding),
	}
}

// logLimitOnce handles usage.ErrLimitReached: it logs the first one each month
// and reports true (handled) for all of them, so skipped messages stay quiet.
func (m *Manager) logLimitOnce(err error) bool {
	var le *usage.LimitError
	if !errors.As(err, &le) {
		return false
	}
	m.limitMu.Lock()
	first := m.limitLogged != le.Month
	m.limitLogged = le.Month
	m.limitMu.Unlock()
	if first {
		log.Printf("tts: %v; messages are skipped until next month", le)
	}
	return true
}

// Join binds textChannelID in guildID to voiceChannelID, joining the voice
// channel. An existing binding in the same guild is replaced; if it is
// already in the same voice channel only the text channel changes.
func (m *Manager) Join(ctx context.Context, guildID, voiceChannelID, textChannelID string) error {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	old := m.bindings[guildID]
	if old != nil && old.voiceChannelID == voiceChannelID {
		old.textChannelID = textChannelID
		m.mu.Unlock()
		return nil
	}
	delete(m.bindings, guildID)
	m.mu.Unlock()

	// discordgo keeps one voice connection per guild, so the old one must
	// be torn down before joining again.
	if old != nil {
		old.close()
	}

	conn, err := m.cfg.Joiner.Join(ctx, guildID, voiceChannelID)
	if err != nil {
		return err
	}
	b := &binding{
		textChannelID:  textChannelID,
		voiceChannelID: voiceChannelID,
		conn:           conn,
		player: voice.NewPlayer(voice.PlayerConfig{
			Synth:     m.cfg.Synth,
			Encoder:   m.cfg.Encoder,
			Conn:      conn,
			Lang:      m.cfg.Lang,
			QueueSize: m.cfg.QueueSize,
			OnError: func(err error) {
				if m.logLimitOnce(err) {
					return
				}
				log.Printf("tts: guild %s: %v", guildID, err)
			},
		}),
	}

	m.mu.Lock()
	m.bindings[guildID] = b
	m.mu.Unlock()
	return nil
}

// Leave stops reading in guildID and leaves the voice channel.
func (m *Manager) Leave(guildID string) error {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	b := m.bindings[guildID]
	delete(m.bindings, guildID)
	m.mu.Unlock()

	if b == nil {
		return ErrNotBound
	}
	return b.close()
}

// Bound returns the text and voice channel bound in guildID, if any.
func (m *Manager) Bound(guildID string) (textChannelID, voiceChannelID string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.bindings[guildID]
	if b == nil {
		return "", "", false
	}
	return b.textChannelID, b.voiceChannelID, true
}

// HandleMessage queues msg for speech if it was sent in a bound text channel
// and survives filtering. It reports whether something was queued.
func (m *Manager) HandleMessage(msg Message, names Names) bool {
	if !ShouldSpeak(msg) {
		return false
	}

	m.mu.Lock()
	b := m.bindings[msg.GuildID]
	if b == nil || b.textChannelID != msg.ChannelID {
		m.mu.Unlock()
		return false
	}
	player := b.player
	m.mu.Unlock()

	text := Normalize(msg.Content, TextOptions{
		MaxChars: m.cfg.MaxChars,
		LinkWord: m.linkWord,
		Names:    names,
	})
	if text == "" {
		return false
	}
	player.Enqueue(Utterance(msg.AuthorName, text, m.cfg.SpeakAuthor))
	return true
}

// HandleVoiceStateUpdate drops the binding in guildID if the bot itself was
// disconnected or moved out of the bound voice channel, or if no humans remain
// in that channel. Call it on every voice state change in the guild.
func (m *Manager) HandleVoiceStateUpdate(guildID string) {
	m.mu.Lock()
	b := m.bindings[guildID]
	m.mu.Unlock()
	if b == nil || m.cfg.Presence == nil {
		return
	}

	switch botChannel := m.cfg.Presence.BotVoiceChannel(guildID); {
	case botChannel == "":
		// Already disconnected from voice: drop the binding and stop the
		// player without calling Disconnect again.
		_ = m.leaveBinding(guildID, b, false)
	case botChannel != b.voiceChannelID:
		// Moved to another channel by a moderator: drop the binding rather
		// than silently rebinding, and leave that channel.
		if err := m.leaveBinding(guildID, b, true); err != nil && !errors.Is(err, ErrNotBound) {
			log.Printf("tts: guild %s: auto-leave: %v", guildID, err)
		}
	case m.cfg.Presence.HumansInVoice(guildID, b.voiceChannelID) == 0:
		if err := m.leaveBinding(guildID, b, true); err != nil && !errors.Is(err, ErrNotBound) {
			log.Printf("tts: guild %s: auto-leave: %v", guildID, err)
		}
	}
}

func (m *Manager) leaveBinding(guildID string, b *binding, disconnect bool) error {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	if m.bindings[guildID] != b {
		m.mu.Unlock()
		return ErrNotBound
	}
	delete(m.bindings, guildID)
	m.mu.Unlock()

	b.player.Close()
	if !disconnect {
		return nil
	}
	return b.conn.Disconnect()
}

// Close leaves every guild. Use it on shutdown.
func (m *Manager) Close() {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	bindings := m.bindings
	m.bindings = make(map[string]*binding)
	m.mu.Unlock()

	for guildID, b := range bindings {
		if err := b.close(); err != nil {
			log.Printf("tts: guild %s: close: %v", guildID, err)
		}
	}
}

func (b *binding) close() error {
	b.player.Close()
	return b.conn.Disconnect()
}
