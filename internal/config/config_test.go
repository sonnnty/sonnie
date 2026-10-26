package config

import "testing"

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
	want := TTSConfig{Enabled: false, Lang: "th", MaxChars: 200, QueueSize: 20, SpeakAuthor: true}
	if cfg.TTS != want {
		t.Fatalf("expected %+v, got %+v", want, cfg.TTS)
	}
}

func TestLoad_TTSOverrides(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"DISCORD_BOT_TOKEN": "t",
		"TTS_ENABLED":       "true",
		"TTS_LANG":          "en",
		"TTS_MAX_CHARS":     "80",
		"TTS_QUEUE_SIZE":    "5",
		"TTS_SPEAK_AUTHOR":  "false",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := TTSConfig{Enabled: true, Lang: "en", MaxChars: 80, QueueSize: 5, SpeakAuthor: false}
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
		{"TTS_MAX_CHARS", "abc"},
		{"TTS_MAX_CHARS", "0"},
		{"TTS_QUEUE_SIZE", "-3"},
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
