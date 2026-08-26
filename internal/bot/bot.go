package bot

import (
	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
)

type Bot struct {
	Session *discordgo.Session
}

func New(token string) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	return &Bot{Session: session}, nil
}

func (b *Bot) Open() error {
	b.Session.AddHandler(handleInteraction)

	if err := b.Session.Open(); err != nil {
		return err
	}

	return b.registerCommands()
}

func handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
	return b.Session.Close()
}
