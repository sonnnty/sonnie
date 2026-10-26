package bot

import (
	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/voice"
)

func newReadAloud(s *discordgo.Session, cfg config.TTSConfig) *readaloud.Manager {
	return readaloud.New(readaloud.Config{
		// TODO: replace both stubs; until then /tts join connects but
		// every message fails with "no synthesizer configured".
		Synth:    tts.Unimplemented{},
		Encoder:  voice.UnimplementedEncoder{},
		Joiner:   voice.DiscordJoiner{Session: s},
		Presence: statePresence{s: s},

		Lang:        cfg.Lang,
		MaxChars:    cfg.MaxChars,
		QueueSize:   cfg.QueueSize,
		SpeakAuthor: cfg.SpeakAuthor,
	})
}

func (b *Bot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.GuildID == "" || m.Author == nil {
		return
	}
	b.readAloud.HandleMessage(readaloud.Message{
		GuildID:    m.GuildID,
		ChannelID:  m.ChannelID,
		AuthorName: authorName(m.Message),
		Content:    m.Content,
		IsBot:      m.Author.Bot || m.WebhookID != "" || (s.State.User != nil && m.Author.ID == s.State.User.ID),
	}, messageNames{s: s, m: m.Message})
}

func (b *Bot) handleVoiceStateUpdate(_ *discordgo.Session, v *discordgo.VoiceStateUpdate) {
	if v.GuildID == "" {
		return
	}
	b.readAloud.HandleVoiceStateUpdate(v.GuildID)
}

func authorName(m *discordgo.Message) string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	return m.Author.DisplayName()
}

// messageNames resolves mentions from the message itself and the state
// cache. Anything not cached is dropped rather than fetched over REST.
type messageNames struct {
	s *discordgo.Session
	m *discordgo.Message
}

func (n messageNames) User(id string) string {
	if member, err := n.s.State.Member(n.m.GuildID, id); err == nil && member.Nick != "" {
		return member.Nick
	}
	for _, u := range n.m.Mentions {
		if u.ID == id {
			return u.DisplayName()
		}
	}
	return ""
}

func (n messageNames) Role(id string) string {
	if role, err := n.s.State.Role(n.m.GuildID, id); err == nil {
		return role.Name
	}
	return ""
}

func (n messageNames) Channel(id string) string {
	if ch, err := n.s.State.Channel(id); err == nil {
		return ch.Name
	}
	return ""
}

// statePresence counts humans in a voice channel from the state cache.
type statePresence struct {
	s *discordgo.Session
}

func (p statePresence) HumansInVoice(guildID, channelID string) int {
	guild, err := p.s.State.Guild(guildID)
	if err != nil {
		// Unknown guild: assume someone is there rather than leave early.
		return 1
	}

	var userIDs []string
	p.s.State.RLock()
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID == channelID {
			userIDs = append(userIDs, vs.UserID)
		}
	}
	p.s.State.RUnlock()

	humans := 0
	for _, id := range userIDs {
		member, err := p.s.State.Member(guildID, id)
		if err != nil || member.User == nil || !member.User.Bot {
			// Members missing from the cache count as humans.
			humans++
		}
	}
	return humans
}
