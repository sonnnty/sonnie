package bot

import (
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/voice"
)

// newReadAloud builds the read-aloud manager. The returned meter is nil when
// no real synthesizer is configured (nothing is billed then).
func newReadAloud(s *discordgo.Session, cfg config.TTSConfig) (*readaloud.Manager, *usage.Meter) {
	synth, encoder := newSpeechPipeline(cfg)
	var meter *usage.Meter
	if _, ok := synth.(*tts.Google); ok {
		meter = newMeter(s, cfg, synth)
		synth = meter
	}
	return readaloud.New(readaloud.Config{
		Synth:   synth,
		Encoder: encoder,
		// Registers its own voice event handlers on s.
		Joiner:   voice.NewDAVEJoiner(s),
		Presence: statePresence{s: s},

		Lang:        cfg.Lang,
		MaxChars:    cfg.MaxChars,
		QueueSize:   cfg.QueueSize,
		SpeakAuthor: cfg.SpeakAuthor,
	}), meter
}

func newMeter(s *discordgo.Session, cfg config.TTSConfig, inner tts.Synthesizer) *usage.Meter {
	mc := usage.Config{Inner: inner, Limit: cfg.MonthlyCharLimit}
	if cfg.AlertChannelID != "" {
		mc.Channel = newDiscordUsageChannel(s, cfg.AlertChannelID)
	}
	return usage.New(mc)
}

func newSpeechPipeline(cfg config.TTSConfig) (tts.Synthesizer, voice.Encoder) {
	if cfg.GoogleAPIKey == "" {
		log.Println("tts: GOOGLE_TTS_API_KEY is not set; /tts join will connect to voice, but speech synthesis is disabled")
		return tts.Unimplemented{}, voice.UnimplementedEncoder{}
	}
	return &tts.Google{
		APIKey: cfg.GoogleAPIKey,
		Voice:  cfg.Voice,
	}, voice.OggOpusEncoder{}
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

// statePresence reports voice channel occupancy from the state cache.
type statePresence struct {
	s *discordgo.Session
}

func (p statePresence) BotVoiceChannel(guildID string) string {
	guild, err := p.s.State.Guild(guildID)
	if err != nil {
		return ""
	}

	p.s.State.RLock()
	defer p.s.State.RUnlock()
	if p.s.State.User == nil {
		return ""
	}
	for _, vs := range guild.VoiceStates {
		if vs.UserID == p.s.State.User.ID {
			return vs.ChannelID
		}
	}
	return ""
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
