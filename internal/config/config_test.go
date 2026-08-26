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
