package bot

import (
	"context"
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
	switch synth.(type) {
	case *tts.Google, *tts.GoogleStream:
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

// newMeter picks the meter mode from config, once per process: money for a
// Gemini model (priced per token, so characters say little), characters
// otherwise.
func newMeter(s *discordgo.Session, cfg config.TTSConfig, inner tts.Synthesizer) *usage.Meter {
	mc := usage.Config{Inner: inner, Limit: cfg.MonthlyCharLimit}
	if tts.IsGeminiModel(cfg.Model) {
		mc.Mode = usage.ModeMoney
		mc.Limit = usage.MicroTHB(cfg.MonthlyBudgetTHB)
		mc.USDToTHB = cfg.USDToTHB
		mc.Prompt = cfg.Prompt
	}
	if cfg.AlertChannelID != "" {
		mc.Channel = newDiscordUsageChannel(s, cfg.AlertChannelID)
	}
	return usage.New(mc)
}

func newSpeechPipeline(cfg config.TTSConfig) (tts.Synthesizer, voice.Encoder) {
	g := &tts.Google{
		Voice:  cfg.Voice,
		Model:  cfg.Model,
		Prompt: cfg.Prompt,
	}
	streaming := cfg.Streaming && tts.IsGeminiModel(cfg.Model)
	switch {
	case cfg.GoogleCredentialsFile != "":
		ctx := context.Background()
		creds, err := tts.LoadGoogleCredentials(ctx, cfg.GoogleCredentialsFile)
		if err != nil {
			log.Printf("%v; speech synthesis is disabled", err)
			return tts.Unimplemented{}, voice.UnimplementedEncoder{}
		}
		g.Client = tts.NewGoogleOAuthClientFromCredentials(ctx, creds)
		if !streaming {
			break
		}
		// The gRPC connection lives as long as the process, like the
		// REST client; nothing closes it before exit.
		open, _, err := tts.NewGoogleStreamOpener(ctx, creds)
		if err != nil {
			log.Printf("%v; using the REST API instead of streaming", err)
			break
		}
		log.Printf("tts: streaming %s over gRPC (TTS_STREAMING); the REST API is the fallback", g.Model)
		return &tts.GoogleStream{REST: g, Open: open}, voice.OggOpusEncoder{}
	case cfg.GoogleAPIKey != "":
		g.APIKey = cfg.GoogleAPIKey
		if streaming {
			log.Println("tts: TTS_STREAMING needs GOOGLE_APPLICATION_CREDENTIALS (a service account); using the REST API with the API key")
		}
	default:
		log.Println("tts: neither GOOGLE_APPLICATION_CREDENTIALS nor GOOGLE_TTS_API_KEY is set; /tts join will connect to voice, but speech synthesis is disabled")
		return tts.Unimplemented{}, voice.UnimplementedEncoder{}
	}
	return g, voice.OggOpusEncoder{}
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
