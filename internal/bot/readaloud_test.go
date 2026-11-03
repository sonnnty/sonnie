package bot

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/voice"
)

func TestNewSpeechPipeline_WithGoogleAPIKey(t *testing.T) {
	synth, enc := newSpeechPipeline(config.TTSConfig{
		Enabled:      true,
		GoogleAPIKey: "test-key",
		Voice:        "th-TH-Chirp3-HD-Achird",
	})
	g, ok := synth.(*tts.Google)
	if !ok {
		t.Fatalf("expected *tts.Google, got %T", synth)
	}
	if g.APIKey != "test-key" || g.Voice != "th-TH-Chirp3-HD-Achird" {
		t.Fatalf("unexpected Google config: %+v", g)
	}
	if _, ok := enc.(voice.OggOpusEncoder); !ok {
		t.Fatalf("expected voice.OggOpusEncoder, got %T", enc)
	}
}

func TestNewSpeechPipeline_WithoutKeyLogsWarningAndKeepsStubs(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	synth, enc := newSpeechPipeline(config.TTSConfig{Enabled: true})
	if _, ok := synth.(tts.Unimplemented); !ok {
		t.Fatalf("expected tts.Unimplemented, got %T", synth)
	}
	if _, ok := enc.(voice.UnimplementedEncoder); !ok {
		t.Fatalf("expected voice.UnimplementedEncoder, got %T", enc)
	}
	if !strings.Contains(buf.String(), "GOOGLE_TTS_API_KEY") {
		t.Fatalf("expected startup warning mentioning GOOGLE_TTS_API_KEY, got %q", buf.String())
	}
}

func TestBotNew_DoesNotCrashWhenTTSEnabledWithoutAPIKey(t *testing.T) {
	b, err := New(config.Config{
		DiscordToken: "test-discord-token",
		TTS: config.TTSConfig{
			Enabled:   true,
			Lang:      "th",
			MaxChars:  200,
			QueueSize: 20,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.readAloud == nil {
		t.Fatal("expected readAloud manager to be initialized")
	}
}
