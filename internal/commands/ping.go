package commands

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register(Command{
		Definition: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with pong",
		},
		Handler: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "pong",
				},
			}); err != nil {
				log.Printf("ping: failed to respond to interaction: %v", err)
			}
		},
	})
}
