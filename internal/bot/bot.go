package bot

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/events"
	"github.com/Sonty15/SONNIE/internal/logring"
	"github.com/Sonty15/SONNIE/internal/modal"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/webhook"
)

type Bot struct {
	Session *discordgo.Session

	// readAloud is nil when the TTS feature is disabled.
	readAloud *readaloud.Manager
	// meter counts Google TTS characters, or money for Gemini models; nil when no Google key is set.
	meter *usage.Meter

	// eventSched reminds and starts/ends scheduled events; stopEvents
	// stops its loop.
	eventSched *events.Scheduler
	stopEvents context.CancelFunc
	// covers is the cover provider chain, for the dashboard.
	covers cover.Chain

	// dash is the HTTP server (dashboard, /healthz, webhooks); nil when
	// DASHBOARD_ADDR is empty. hooks is nil without WEBHOOKS.
	dash  *dashboard.Server
	hooks *webhook.Handler
	// logs holds the recent log lines; nil in tests.
	logs *logring.Ring

	started   time.Time
	connected atomic.Bool  // the gateway is connected (Ready/Resumed until Disconnect)
	appID     atomic.Value // string: the application ID from Ready
}

// New builds the bot. logs, which may be nil, is the ring the standard
// logger is teed into; the dashboard shows it.
func New(cfg config.Config, logs *logring.Ring) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	b := &Bot{Session: session, logs: logs, started: time.Now()}

	// Scheduled events (/event). Not privileged, and already in discordgo's
	// default intents; set explicitly because the feature depends on it.
	session.Identify.Intents |= discordgo.IntentsGuildScheduledEvents
	var eventHandler *events.Handler
	eventHandler, b.eventSched, b.covers = newEvents(session, cfg.Events)
	commands.SetEvents(eventHandler)

	if cfg.TTS.Enabled {
		// Message Content is privileged: it must also be switched on in the
		// Developer Portal, or the gateway refuses the connection. Guild
		// messages and voice states are already in the default intents.
		session.Identify.Intents |= discordgo.IntentsGuildMessages |
			discordgo.IntentsGuildVoiceStates |
			discordgo.IntentsMessageContent
		b.readAloud, b.meter = newReadAloud(session, cfg.TTS)
		commands.SetTTS(b.readAloud)
		if b.meter != nil {
			commands.SetUsage(b.meter)
		}
	}

	// Last, so the panels see every feature.
	if b.dash, err = b.newDashboard(cfg); err != nil {
		return nil, err
	}

	return b, nil
}

func (b *Bot) Open() error {
	b.Session.AddHandler(b.handleReady)
	b.Session.AddHandler(b.handleResumed)
	b.Session.AddHandler(b.handleDisconnect)
	b.Session.AddHandler(b.handleScheduledEventUserAdd)
	b.Session.AddHandler(b.handleScheduledEventUserRemove)
	b.Session.AddHandler(handleInteraction)
	b.Session.AddHandler(handleModalSubmit)
	b.Session.AddHandler(b.handleEventsGuildCreate)
	b.Session.AddHandler(b.handleScheduledEventCreate)
	b.Session.AddHandler(b.handleScheduledEventUpdate)
	b.Session.AddHandler(b.handleScheduledEventDelete)
	if b.readAloud != nil {
		b.Session.AddHandler(b.handleMessageCreate)
		b.Session.AddHandler(b.handleVoiceStateUpdate)
		b.Session.AddHandler(b.handleGuildCreate)
	}

	if err := b.startDashboard(); err != nil {
		return err
	}
	if err := b.Session.Open(); err != nil {
		return err
	}

	if b.meter != nil {
		// Needs the session open: the bot's own user ID identifies its
		// status message in the alert channel.
		b.meter.Start(context.Background())
	}

	// Guilds (and their events) arrive after Open; the loop waits for them.
	ctx, cancel := context.WithCancel(context.Background())
	b.stopEvents = cancel
	go b.eventSched.Run(ctx)

	return b.registerCommands()
}

func handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if c, ok := commands.Find(i.ApplicationCommandData().Name); ok {
			c.Handler(s, i)
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		if c, ok := commands.Find(i.ApplicationCommandData().Name); ok && c.Autocomplete != nil {
			c.Autocomplete(s, i)
		}
	case discordgo.InteractionMessageComponent:
		if c, ok := commands.FindComponent(i.MessageComponentData().CustomID); ok {
			c.Component(s, i)
		}
	}
	// Modal submits are left to handleModalSubmit.
}

// handleModalSubmit routes submitted modals. It reads the raw gateway
// event because discordgo v0.29 cannot decode modals made of Label
// components: it logs "error unmarshalling INTERACTION_CREATE event,
// ... unknown component type: 18" and passes on the interaction without
// data. That line is expected for every submitted /event form.
func handleModalSubmit(s *discordgo.Session, e *discordgo.Event) {
	if e.Type != "INTERACTION_CREATE" {
		return
	}
	i, m, ok, err := modal.Decode(e.RawData)
	if err != nil {
		log.Printf("modal submit: %v", err)
		return
	}
	if !ok {
		return
	}
	if c, found := commands.FindModal(m.CustomID); found {
		c.Modal(s, i, m)
	}
}

func (b *Bot) registerCommands() error {
	for _, c := range commands.Registry {
		if _, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", c.Definition); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) Close() error {
	// First, so no webhook posts into a closing session.
	b.stopDashboard()
	if b.stopEvents != nil {
		b.stopEvents()
	}
	if b.readAloud != nil {
		b.readAloud.Close()
	}
	if b.meter != nil {
		// After readAloud.Close so no more text is counted; writes the
		// final count to the alert channel.
		b.meter.Close()
	}
	return b.Session.Close()
}
