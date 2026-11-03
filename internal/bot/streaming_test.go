package bot

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/voice"
)

// fakeServiceAccount writes a throwaway service account key file (a key
// generated for the test, never used against Google).
func fakeServiceAccount(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "test",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "sonnie@test-project.iam.gserviceaccount.com",
		"token_uri":      "https://oauth2.invalid/token",
	})
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewSpeechPipeline_GeminiStreamsByDefault(t *testing.T) {
	cfg := config.TTSConfig{
		GoogleCredentialsFile: fakeServiceAccount(t),
		Model:                 "gemini-2.5-flash-tts", Voice: "Kore", Prompt: "calm",
		Streaming: true,
	}
	synth, enc := newSpeechPipeline(cfg)
	gs, ok := synth.(*tts.GoogleStream)
	if !ok {
		t.Fatalf("expected *tts.GoogleStream, got %T", synth)
	}
	if gs.Open == nil || gs.REST == nil || gs.REST.Client == nil || gs.REST.Model != cfg.Model || gs.REST.Voice != "Kore" || gs.REST.Prompt != "calm" {
		t.Fatalf("unexpected stream config: %+v / %+v", gs, gs.REST)
	}
	if _, ok := enc.(voice.StreamEncoder); !ok {
		t.Fatalf("encoder %T cannot stream", enc)
	}
}

func TestNewSpeechPipeline_StreamingOffOrNotGeminiUsesREST(t *testing.T) {
	sa := fakeServiceAccount(t)
	for name, cfg := range map[string]config.TTSConfig{
		"gemini, TTS_STREAMING=false": {GoogleCredentialsFile: sa, Model: "gemini-2.5-flash-tts", Streaming: false},
		"chirp, TTS_STREAMING=true":   {GoogleCredentialsFile: sa, Voice: "th-TH-Chirp3-HD-Achird", Streaming: true},
	} {
		synth, _ := newSpeechPipeline(cfg)
		if g, ok := synth.(*tts.Google); !ok || g.Client == nil {
			t.Errorf("%s: expected an authenticated *tts.Google, got %#v", name, synth)
		}
	}
}

func TestNewSpeechPipeline_StreamingWithAPIKeyWarnsAndUsesREST(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	synth, _ := newSpeechPipeline(config.TTSConfig{GoogleAPIKey: "k", Model: "gemini-2.5-flash-tts", Streaming: true})
	if _, ok := synth.(*tts.Google); !ok {
		t.Fatalf("expected *tts.Google, got %T", synth)
	}
	if !strings.Contains(buf.String(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Fatalf("missing warning: %q", buf.String())
	}
}

func TestBotNew_StreamingSynthIsMetered(t *testing.T) {
	b, err := New(config.Config{DiscordToken: "t", TTS: config.TTSConfig{
		Enabled: true, GoogleCredentialsFile: fakeServiceAccount(t), Model: "gemini-2.5-flash-tts", Streaming: true,
		Lang: "th", MaxChars: 200, QueueSize: 20, MonthlyBudgetTHB: 300, USDToTHB: 33,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if b.meter == nil {
		t.Fatal("streaming synthesizer is not metered")
	}
	if s := b.meter.Snapshot(); s.Unit != usage.UnitTHBMicro {
		t.Fatalf("snapshot %+v, want the money meter", s)
	}
}
