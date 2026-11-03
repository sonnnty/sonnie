package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Sonty15/SONNIE/internal/tts"
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
	// GoogleAPIKey is the Google Cloud Text-to-Speech API key
	// (GOOGLE_TTS_API_KEY). When empty, speech synthesis stays stubbed.
	GoogleAPIKey string
	// GoogleCredentialsFile is a service account JSON key file
	// (GOOGLE_APPLICATION_CREDENTIALS). Gemini-TTS needs it: the endpoint
	// rejects API keys for Gemini models. It takes precedence over
	// GoogleAPIKey when both are set.
	GoogleCredentialsFile string
	// Lang is the language code passed to the synthesizer.
	Lang string
	// Voice, if non-empty, overrides the default voice name selected from
	// Lang (e.g. "th-TH-Chirp3-HD-Achird").
	Voice string
	// Model, if non-empty (TTS_MODEL), selects a Gemini-TTS model such as
	// "gemini-2.5-flash-tts". Empty keeps the classic voices (Chirp/Neural2).
	Model string
	// Prompt is an optional style prompt (TTS_PROMPT), sent only to Gemini
	// models.
	Prompt string
	// Streaming (TTS_STREAMING) synthesizes Gemini models over the gRPC
	// streaming API so speech starts before the whole sentence is ready.
	// It defaults to true for a Gemini model; other voices always use the
	// unary REST call.
	Streaming bool
	// MonthlyBudgetTHB is the money cap for Gemini models in baht per
	// US/Pacific month (TTS_MONTHLY_BUDGET_THB). 0 means no cap.
	MonthlyBudgetTHB float64
	// USDToTHB converts the USD list price into baht (TTS_USD_TO_THB).
	USDToTHB float64
	// MaxChars caps how many characters of a single message are spoken.
	MaxChars int
	// QueueSize caps how many messages may wait per guild; the oldest
	// waiting message is dropped when it is full.
	QueueSize int
	// SpeakAuthor prefixes each message with the author's display name.
	SpeakAuthor bool
	// MonthlyCharLimit is the most characters sent to Google TTS per
	// US/Pacific calendar month (TTS_MONTHLY_CHAR_LIMIT). 0 means no cap.
	MonthlyCharLimit int64
	// AlertChannelID, if set (TTS_ALERT_CHANNEL_ID), is the Discord channel
	// that receives usage alerts and holds the persisted usage counter.
	AlertChannelID string
}

const (
	defaultTTSLang      = "th"
	defaultTTSMaxChars  = 200
	defaultTTSQueueSize = 20

	defaultTTSMonthlyBudgetTHB = 300
	defaultTTSUSDToTHB         = 33

	// defaultTTSMonthlyCharLimit is Google's free tier for Chirp 3: HD.
	defaultTTSMonthlyCharLimit = 1000000
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

		MonthlyCharLimit: defaultTTSMonthlyCharLimit,
		MonthlyBudgetTHB: defaultTTSMonthlyBudgetTHB,
		USDToTHB:         defaultTTSUSDToTHB,
	}

	if v := getenv("TTS_LANG"); v != "" {
		cfg.Lang = v
	}
	cfg.Voice = getenv("TTS_VOICE")
	cfg.GoogleAPIKey = getenv("GOOGLE_TTS_API_KEY")
	cfg.GoogleCredentialsFile = strings.TrimSpace(getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	cfg.Model = strings.TrimSpace(getenv("TTS_MODEL"))
	cfg.Prompt = strings.TrimSpace(getenv("TTS_PROMPT"))

	var err error
	if cfg.Enabled, err = parseBool(getenv, "TTS_ENABLED", false); err != nil {
		return TTSConfig{}, err
	}
	if cfg.Streaming, err = parseBool(getenv, "TTS_STREAMING", tts.IsGeminiModel(cfg.Model)); err != nil {
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

	if cfg.MonthlyCharLimit, err = parseNonNegativeInt64(getenv, "TTS_MONTHLY_CHAR_LIMIT", cfg.MonthlyCharLimit); err != nil {
		return TTSConfig{}, err
	}
	if cfg.MonthlyBudgetTHB, err = parseNonNegativeFloat(getenv, "TTS_MONTHLY_BUDGET_THB", cfg.MonthlyBudgetTHB); err != nil {
		return TTSConfig{}, err
	}
	if cfg.USDToTHB, err = parsePositiveFloat(getenv, "TTS_USD_TO_THB", cfg.USDToTHB); err != nil {
		return TTSConfig{}, err
	}
	if cfg.AlertChannelID = strings.TrimSpace(getenv("TTS_ALERT_CHANNEL_ID")); cfg.AlertChannelID != "" && !isSnowflake(cfg.AlertChannelID) {
		return TTSConfig{}, fmt.Errorf("TTS_ALERT_CHANNEL_ID: %q is not a Discord channel ID", cfg.AlertChannelID)
	}

	return cfg, nil
}

func isSnowflake(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func parseNonNegativeInt64(getenv func(string) string, key string, def int64) (int64, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: %q is not a non-negative integer", key, v)
	}
	return n, nil
}

func parseNonNegativeFloat(getenv func(string) string, key string, def float64) (float64, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, fmt.Errorf("%s: %q is not a non-negative number", key, v)
	}
	return f, nil
}

func parsePositiveFloat(getenv func(string) string, key string, def float64) (float64, error) {
	f, err := parseNonNegativeFloat(getenv, key, def)
	if err != nil {
		return 0, err
	}
	if f == 0 {
		return 0, fmt.Errorf("%s: must be greater than 0", key)
	}
	return f, nil
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
