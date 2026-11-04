package commands

import (
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/events"
	"github.com/Sonty15/SONNIE/internal/modal"
)

// EventHandler is what /event needs from the events feature.
type EventHandler interface {
	HandleCommand(i *discordgo.InteractionCreate)
	HandleAutocomplete(i *discordgo.InteractionCreate)
	HandleComponent(i *discordgo.InteractionCreate)
	HandleModalSubmit(i *discordgo.InteractionCreate, m modal.Submit)
}

// eventHandler is set by SetEvents during startup.
var eventHandler EventHandler

// SetEvents gives /event its handler. Same rules as SetTTS.
func SetEvents(h EventHandler) {
	eventHandler = h
}

func init() {
	register(Command{
		Definition: events.CommandDefinition(),
		Handler: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if eventHandler == nil {
				eventsNotReady(s, i)
				return
			}
			eventHandler.HandleCommand(i)
		},
		Autocomplete: func(_ *discordgo.Session, i *discordgo.InteractionCreate) {
			if eventHandler != nil {
				eventHandler.HandleAutocomplete(i)
			}
		},
		ComponentPrefix: events.CustomIDPrefix,
		Component: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if eventHandler == nil {
				eventsNotReady(s, i)
				return
			}
			eventHandler.HandleComponent(i)
		},
		Modal: func(s *discordgo.Session, i *discordgo.InteractionCreate, m modal.Submit) {
			if eventHandler == nil {
				eventsNotReady(s, i)
				return
			}
			eventHandler.HandleModalSubmit(i, m)
		},
	})
}

func eventsNotReady(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "ระบบอีเวนต์ยังไม่พร้อม ลองใหม่อีกครั้ง", Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		log.Printf("event: failed to respond to interaction: %v", err)
	}
}
