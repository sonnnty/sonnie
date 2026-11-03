package commands

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Command struct {
	Definition *discordgo.ApplicationCommand
	Handler    func(s *discordgo.Session, i *discordgo.InteractionCreate)
	// Autocomplete, if set, answers autocomplete for the command's options.
	Autocomplete func(s *discordgo.Session, i *discordgo.InteractionCreate)
	// Component, if set, answers clicks on buttons whose custom ID starts
	// with ComponentPrefix.
	ComponentPrefix string
	Component       func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

var Registry []Command

func register(c Command) {
	Registry = append(Registry, c)
}

func Find(name string) (Command, bool) {
	for _, c := range Registry {
		if c.Definition.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// FindComponent returns the command whose buttons use customID.
func FindComponent(customID string) (Command, bool) {
	for _, c := range Registry {
		if c.Component != nil && c.ComponentPrefix != "" && strings.HasPrefix(customID, c.ComponentPrefix) {
			return c, true
		}
	}
	return Command{}, false
}
