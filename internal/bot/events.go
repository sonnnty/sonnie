package bot

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/oauth2"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/events"
	"github.com/Sonty15/SONNIE/internal/tts"
)

// imageHTTPTimeout bounds each request to an image library or image host.
const imageHTTPTimeout = 15 * time.Second

// newEvents builds /event: the cover finder (Brave, Unsplash and Pexels
// when they have a key, then Openverse; Gemini keywords when a service
// account is set) and the scheduler for reminders and automatic start/end.
// The provider chain is returned for the dashboard.
func newEvents(s *discordgo.Session, cfg config.EventsConfig) (*events.Handler, *events.Scheduler, cover.Chain) {
	client := &http.Client{Timeout: imageHTTPTimeout}
	fallback := "Openverse"
	if cfg.UnsplashKey != "" {
		fallback = "Unsplash"
	}
	finder := &cover.Finder{
		Providers: cover.NewChain(client, cfg.PexelsKey, cfg.UnsplashKey, cfg.BraveKey, braveNotifier{s: s, channelID: cfg.ChannelID, fallback: fallback}),
		Client:    client,
		Keywords:  newKeyworder(cfg.GoogleCredentialsFile),
	}
	sched := events.NewScheduler(events.SchedulerConfig{
		API:          s,
		BotID:        func() string { return botUserID(s) },
		ChannelID:    cfg.ChannelID,
		RemindBefore: cfg.RemindBefore,
		AutoStart:    cfg.AutoStart,
	})

	remind := "off"
	if cfg.ChannelID != "" && cfg.RemindBefore > 0 {
		remind = cfg.RemindBefore.String() + " before, in channel " + cfg.ChannelID
	}
	log.Printf("events: covers from %s; Thai keywords via Gemini: %v; reminders: %s; auto start for others' events: %v",
		strings.Join(finder.Providers.Names(), " → "), finder.Keywords != nil, remind, cfg.AutoStart)
	h := events.NewHandler(s, sched, finder, nil)
	h.SetVoiceChannel(func(guildID, userID string) string { return voiceChannelOf(s.State, guildID, userID) })
	return h, sched, finder.Providers
}

// voiceChannelOf is the voice channel userID is in, from the gateway
// state (voice states are in the default intents), or "" when they are
// in none or in a stage, which the form's channel select does not offer.
func voiceChannelOf(state *discordgo.State, guildID, userID string) string {
	vs, err := state.VoiceState(guildID, userID)
	if err != nil || vs.ChannelID == "" {
		return ""
	}
	ch, err := state.Channel(vs.ChannelID)
	if err != nil || ch.Type != discordgo.ChannelTypeGuildVoice {
		return ""
	}
	return ch.ID
}

// newKeyworder returns nil (search by the raw name) when there is no
// usable service account.
func newKeyworder(credentialsFile string) cover.Keyworder {
	if credentialsFile == "" {
		return nil
	}
	ctx := context.Background()
	creds, err := tts.LoadGoogleCredentials(ctx, credentialsFile)
	if err != nil {
		log.Printf("events: %v; cover search uses event names as they are", err)
		return nil
	}
	if creds.ProjectID == "" {
		log.Println("events: the service account has no project_id; cover search uses event names as they are")
		return nil
	}
	return &cover.Gemini{Client: oauth2.NewClient(ctx, creds.TokenSource), Project: creds.ProjectID}
}

func botUserID(s *discordgo.Session) string {
	if s.State != nil && s.State.User != nil {
		return s.State.User.ID
	}
	return ""
}

// handleEventsGuildCreate reads the guild's events once it is available,
// at startup and when the bot joins: reminders and starts are scheduled
// from that list.
func (b *Bot) handleEventsGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	if g.Unavailable {
		return
	}
	if err := b.eventSched.LoadGuild(g.ID); err != nil {
		log.Printf("%v (Manage Events may be missing)", err)
	}
}

func (b *Bot) handleScheduledEventCreate(_ *discordgo.Session, e *discordgo.GuildScheduledEventCreate) {
	b.eventSched.Upsert(e.GuildScheduledEvent)
}

func (b *Bot) handleScheduledEventUpdate(_ *discordgo.Session, e *discordgo.GuildScheduledEventUpdate) {
	b.eventSched.Upsert(e.GuildScheduledEvent)
}

func (b *Bot) handleScheduledEventUserAdd(_ *discordgo.Session, e *discordgo.GuildScheduledEventUserAdd) {
	b.eventSched.AdjustInterested(e.GuildScheduledEventID, 1)
}

func (b *Bot) handleScheduledEventUserRemove(_ *discordgo.Session, e *discordgo.GuildScheduledEventUserRemove) {
	b.eventSched.AdjustInterested(e.GuildScheduledEventID, -1)
}

func (b *Bot) handleScheduledEventDelete(_ *discordgo.Session, e *discordgo.GuildScheduledEventDelete) {
	if e.GuildScheduledEvent != nil {
		b.eventSched.Remove(e.ID)
	}
}
