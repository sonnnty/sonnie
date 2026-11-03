package bot

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/usage"
)

type Bot struct {
	Session *discordgo.Session

	// readAloud is nil when the TTS feature is disabled.
	readAloud *readaloud.Manager
	// meter counts Google TTS characters; nil when no Google key is set.
	meter *usage.Meter
}

func New(cfg config.Config) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	b := &Bot{Session: session}

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
	if b.readAloud != nil {
		b.Session.AddHandler(b.handleMessageCreate)
		b.Session.AddHandler(b.handleVoiceStateUpdate)
	}

	if err := b.Session.Open(); err != nil {
		return err
	}

	if b.meter != nil {
		// Needs the session open: the bot's own user ID identifies its
		// status message in the alert channel.
		b.meter.Start(context.Background())
	}

	return b.registerCommands()
}

func handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	c, ok := commands.Find(i.ApplicationCommandData().Name)
	if !ok {
		return
	}
	c.Handler(s, i)
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
