package config

import "errors"

type Config struct {
	DiscordToken string
}

func Load(getenv func(string) string) (Config, error) {
	token := getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		return Config{}, errors.New("DISCORD_BOT_TOKEN is not set")
	}
	return Config{DiscordToken: token}, nil
}
