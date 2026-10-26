package config

import (
	"errors"
	"fmt"
	"strconv"
)

type Config struct {
	DiscordToken string
	TTS          TTSConfig
}

// TTSConfig holds the optional settings for the read-chat-aloud feature.
type TTSConfig struct {
	// Enabled turns the feature on. It is off by default because it needs
	// the privileged Message Content intent, and requesting a privileged
	// intent that is not enabled in the Developer Portal stops the bot
	// from connecting at all.
	Enabled bool
	// Lang is the language code passed to the synthesizer.
	Lang string
	// MaxChars caps how many characters of a single message are spoken.
	MaxChars int
	// QueueSize caps how many messages may wait per guild; the oldest
	// waiting message is dropped when it is full.
	QueueSize int
	// SpeakAuthor prefixes each message with the author's display name.
	SpeakAuthor bool
}

const (
	defaultTTSLang      = "th"
	defaultTTSMaxChars  = 200
	defaultTTSQueueSize = 20
)

func Load(getenv func(string) string) (Config, error) {
	token := getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		return Config{}, errors.New("DISCORD_BOT_TOKEN is not set")
	}

	tts, err := loadTTS(getenv)
	if err != nil {
		return Config{}, err
	}

	return Config{DiscordToken: token, TTS: tts}, nil
}

func loadTTS(getenv func(string) string) (TTSConfig, error) {
	cfg := TTSConfig{
		Lang:        defaultTTSLang,
		MaxChars:    defaultTTSMaxChars,
		QueueSize:   defaultTTSQueueSize,
		SpeakAuthor: true,
	}

	if v := getenv("TTS_LANG"); v != "" {
		cfg.Lang = v
	}

	var err error
	if cfg.Enabled, err = parseBool(getenv, "TTS_ENABLED", false); err != nil {
		return TTSConfig{}, err
	}
	if cfg.SpeakAuthor, err = parseBool(getenv, "TTS_SPEAK_AUTHOR", cfg.SpeakAuthor); err != nil {
		return TTSConfig{}, err
	}
	if cfg.MaxChars, err = parsePositiveInt(getenv, "TTS_MAX_CHARS", cfg.MaxChars); err != nil {
		return TTSConfig{}, err
	}
	if cfg.QueueSize, err = parsePositiveInt(getenv, "TTS_QUEUE_SIZE", cfg.QueueSize); err != nil {
		return TTSConfig{}, err
	}

	return cfg, nil
}

func parseBool(getenv func(string) string, key string, def bool) (bool, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not a boolean", key, v)
	}
	return b, nil
}

func parsePositiveInt(getenv func(string) string, key string, def int) (int, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive integer", key, v)
	}
	return n, nil
}
