package commands

import "github.com/bwmarrin/discordgo"

type Command struct {
	Definition *discordgo.ApplicationCommand
	Handler    func(s *discordgo.Session, i *discordgo.InteractionCreate)
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
