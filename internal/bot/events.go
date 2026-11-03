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

// newEvents builds /event: the cover finder (keyed providers first, then
// Openverse; Gemini keywords when a service account is set) and the
// scheduler for reminders and automatic start/end.
func newEvents(s *discordgo.Session, cfg config.EventsConfig) (*events.Handler, *events.Scheduler) {
	client := &http.Client{Timeout: imageHTTPTimeout}
	finder := &cover.Finder{
		Providers: cover.NewChain(client, cfg.PexelsKey, cfg.UnsplashKey),
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
	return events.NewHandler(s, sched, finder, nil), sched
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

func (b *Bot) handleScheduledEventDelete(_ *discordgo.Session, e *discordgo.GuildScheduledEventDelete) {
	if e.GuildScheduledEvent != nil {
		b.eventSched.Remove(e.ID)
	}
}
