package bot

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/events"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/usage"
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
}

func New(cfg config.Config) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	b := &Bot{Session: session}

	// Scheduled events (/event). Not privileged, and already in discordgo's
	// default intents; set explicitly because the feature depends on it.
	session.Identify.Intents |= discordgo.IntentsGuildScheduledEvents
	var eventHandler *events.Handler
	eventHandler, b.eventSched = newEvents(session, cfg.Events)
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

	return b, nil
}

func (b *Bot) Open() error {
	b.Session.AddHandler(handleInteraction)
	b.Session.AddHandler(b.handleEventsGuildCreate)
	b.Session.AddHandler(b.handleScheduledEventCreate)
	b.Session.AddHandler(b.handleScheduledEventUpdate)
	b.Session.AddHandler(b.handleScheduledEventDelete)
	if b.readAloud != nil {
		b.Session.AddHandler(b.handleMessageCreate)
		b.Session.AddHandler(b.handleVoiceStateUpdate)
		b.Session.AddHandler(b.handleGuildCreate)
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
