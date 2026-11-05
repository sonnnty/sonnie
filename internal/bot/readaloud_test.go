package bot

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
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

func TestBotNew_MeterOnlyWithGoogleKey(t *testing.T) {
	with, err := New(config.Config{DiscordToken: "t", TTS: config.TTSConfig{
		Enabled: true, GoogleAPIKey: "k", Lang: "th", MaxChars: 200, QueueSize: 20,
		MonthlyCharLimit: 1000, AlertChannelID: "123",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if with.meter == nil {
		t.Fatal("expected a usage meter when a Google key is set")
	}
	if s := with.meter.Snapshot(); s.Limit != 1000 {
		t.Fatalf("meter limit = %d, want 1000", s.Limit)
	}

	without, err := New(config.Config{DiscordToken: "t", TTS: config.TTSConfig{Enabled: true, Lang: "th", MaxChars: 200, QueueSize: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if without.meter != nil {
		t.Fatal("no meter expected without a Google key")
	}
}

func TestNewMeter_ModeFollowsModel(t *testing.T) {
	chirp := newMeter(nil, config.TTSConfig{MonthlyCharLimit: 1000, MonthlyBudgetTHB: 300, USDToTHB: 33}, tts.Unimplemented{})
	if s := chirp.Snapshot(); s.Unit != usage.UnitChars || s.Limit != 1000 {
		t.Fatalf("non-Gemini snapshot %+v, want the character meter", s)
	}

	gemini := newMeter(nil, config.TTSConfig{
		Model: "gemini-2.5-flash-tts", MonthlyCharLimit: 1000, MonthlyBudgetTHB: 300, USDToTHB: 33,
	}, tts.Unimplemented{})
	if s := gemini.Snapshot(); s.Unit != usage.UnitTHBMicro || s.Limit != 300000000 {
		t.Fatalf("Gemini snapshot %+v, want a ฿300 money meter", s)
	}
}

func TestNewSpeechPipeline_PassesGeminiSettings(t *testing.T) {
	synth, _ := newSpeechPipeline(config.TTSConfig{
		GoogleAPIKey: "k", Voice: "Charon", Model: "gemini-2.5-flash-tts", Prompt: "friendly",
	})
	g, ok := synth.(*tts.Google)
	if !ok || g.Model != "gemini-2.5-flash-tts" || g.Prompt != "friendly" || g.Voice != "Charon" {
		t.Fatalf("got %#v", synth)
	}
}

func TestBotNew_PassesVoicePoolToReadAloud(t *testing.T) {
	b, err := New(config.Config{
		DiscordToken: "t",
		TTS: config.TTSConfig{
			Enabled:   true,
			Lang:      "th",
			VoicePool: []string{"Puck", "Orus"},
			MaxChars:  200,
			QueueSize: 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(b.readAloud.VoicePool(), ","); got != "Puck,Orus" {
		t.Fatalf("VoicePool = %q, want Puck,Orus", got)
	}
}
