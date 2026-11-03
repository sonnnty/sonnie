package config

import (
	"testing"
	"time"
)

func TestLoad_ReturnsTokenFromEnv(t *testing.T) {
	getenv := func(key string) string {
		if key == "DISCORD_BOT_TOKEN" {
			return "test-token"
		}
		return ""
	}

	cfg, err := Load(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DiscordToken != "test-token" {
		t.Fatalf("expected token %q, got %q", "test-token", cfg.DiscordToken)
	}
}

func TestLoad_ErrorsWhenTokenMissing(t *testing.T) {
	getenv := func(key string) string { return "" }

	_, err := Load(getenv)
	if err == nil {
		t.Fatal("expected error when DISCORD_BOT_TOKEN is missing, got nil")
	}
}

func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoad_TTSDefaults(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := TTSConfig{Enabled: false, Lang: "th", MaxChars: 200, QueueSize: 20, SpeakAuthor: true, DeleteAfter: 8 * time.Second, MonthlyCharLimit: 1000000, MonthlyBudgetTHB: 300, USDToTHB: 33}
	if cfg.TTS != want {
		t.Fatalf("expected %+v, got %+v", want, cfg.TTS)
	}
}

func TestLoad_TTSOverrides(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"DISCORD_BOT_TOKEN":  "t",
		"TTS_ENABLED":        "true",
		"GOOGLE_TTS_API_KEY": "gkey",
		"TTS_LANG":           "en",
		"TTS_VOICE":          "en-US-Chirp3-HD-Charon",
		"TTS_MAX_CHARS":      "80",
		"TTS_QUEUE_SIZE":     "5",
		"TTS_SPEAK_AUTHOR":   "false",
		"TTS_DELETE_AFTER":   "5s",

		"TTS_MONTHLY_CHAR_LIMIT": "500000",
		"TTS_ALERT_CHANNEL_ID":   "123456789012345678",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := TTSConfig{
		Enabled:      true,
		GoogleAPIKey: "gkey",
		Lang:         "en",
		Voice:        "en-US-Chirp3-HD-Charon",
		MaxChars:     80,
		QueueSize:    5,
		SpeakAuthor:  false,
		DeleteAfter:  5 * time.Second,

		MonthlyCharLimit: 500000,
		MonthlyBudgetTHB: 300,
		USDToTHB:         33,
		AlertChannelID:   "123456789012345678",
	}
	if cfg.TTS != want {
		t.Fatalf("expected %+v, got %+v", want, cfg.TTS)
	}
}

func TestLoad_TTSInvalidValues(t *testing.T) {
	tests := []struct {
		key, value string
	}{
		{"TTS_ENABLED", "maybe"},
		{"TTS_SPEAK_AUTHOR", "yes please"},
		{"TTS_STREAMING", "sometimes"},
		{"TTS_MAX_CHARS", "abc"},
		{"TTS_MAX_CHARS", "0"},
		{"TTS_QUEUE_SIZE", "-3"},
		{"TTS_DELETE_AFTER", "-1s"},
		{"TTS_DELETE_AFTER", "soon"},
		{"TTS_DELETE_AFTER", "8"},
		{"TTS_MONTHLY_CHAR_LIMIT", "-1"},
		{"TTS_MONTHLY_CHAR_LIMIT", "1m"},
		{"TTS_ALERT_CHANNEL_ID", "#alerts"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			_, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", tt.key: tt.value}))
			if err == nil {
				t.Fatalf("expected error for %s=%q, got nil", tt.key, tt.value)
			}
		})
	}
}

func TestLoad_TTSMonthlyLimitZeroMeansNoCap(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", "TTS_MONTHLY_CHAR_LIMIT": "0"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TTS.MonthlyCharLimit != 0 {
		t.Fatalf("expected 0 (no cap), got %d", cfg.TTS.MonthlyCharLimit)
	}
}

func TestLoad_GeminiSettings(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"DISCORD_BOT_TOKEN":      "t",
		"TTS_MODEL":              " gemini-2.5-flash-tts ",
		"TTS_VOICE":              "Charon",
		"TTS_PROMPT":             "พูดแบบเป็นกันเอง",
		"TTS_MONTHLY_BUDGET_THB": "250.5",
		"TTS_USD_TO_THB":         "34.25",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.TTS
	if got.Model != "gemini-2.5-flash-tts" || got.Voice != "Charon" || got.Prompt != "พูดแบบเป็นกันเอง" ||
		got.MonthlyBudgetTHB != 250.5 || got.USDToTHB != 34.25 {
		t.Fatalf("unexpected gemini settings: %+v", got)
	}
}

func TestLoad_StreamingDefaultsToGeminiModel(t *testing.T) {
	for _, tt := range []struct {
		model, streaming string
		want             bool
	}{
		{"gemini-2.5-flash-tts", "", true},
		{"gemini-2.5-flash-tts", "false", false},
		{"", "", false},
		// Accepted, but the bot ignores it for non-Gemini voices.
		{"", "true", true},
	} {
		cfg, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", "TTS_MODEL": tt.model, "TTS_STREAMING": tt.streaming}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TTS.Streaming != tt.want {
			t.Errorf("TTS_MODEL=%q TTS_STREAMING=%q: Streaming = %v, want %v", tt.model, tt.streaming, cfg.TTS.Streaming, tt.want)
		}
	}
}

func TestLoad_InvalidMoneySettings(t *testing.T) {
	for _, tt := range []struct{ key, val string }{
		{"TTS_MONTHLY_BUDGET_THB", "-1"},
		{"TTS_MONTHLY_BUDGET_THB", "abc"},
		{"TTS_MONTHLY_BUDGET_THB", "NaN"},
		{"TTS_USD_TO_THB", "0"},
		{"TTS_USD_TO_THB", "-33"},
		{"TTS_USD_TO_THB", "Inf"},
	} {
		if _, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", tt.key: tt.val})); err == nil {
			t.Errorf("%s=%s: expected error", tt.key, tt.val)
		}
	}
}

func TestLoad_TTSDeleteAfterZeroTurnsItOff(t *testing.T) {
	for _, v := range []string{"0", "0s"} {
		cfg, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", "TTS_DELETE_AFTER": v}))
		if err != nil {
			t.Fatalf("TTS_DELETE_AFTER=%s: %v", v, err)
		}
		if cfg.TTS.DeleteAfter != 0 {
			t.Fatalf("TTS_DELETE_AFTER=%s: DeleteAfter = %v, want 0", v, cfg.TTS.DeleteAfter)
		}
	}
}
