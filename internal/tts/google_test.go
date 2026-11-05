package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoogle_SynthesizeThaiAndInspectRequest(t *testing.T) {
	const apiKey = "secret-google-key-123"
	wantAudio := []byte("OggS-fake-opus-stream")

	var (
		gotMethod string
		gotURL    string
		gotKeyHdr string
		gotCT     string
		gotReq    googleRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotURL = r.URL.String()
		gotKeyHdr = r.Header.Get("X-Goog-Api-Key")
		gotCT = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString(wantAudio) + `"}`))
	}))
	defer srv.Close()

	g := &Google{
		APIKey:   apiKey,
		Endpoint: srv.URL + "/v1/text:synthesize",
		Client:   srv.Client(),
	}
	audio, err := g.Synthesize(context.Background(), "สวัสดีครับ", "th")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if audio.Format != FormatOggOpus {
		t.Fatalf("expected format %q, got %q", FormatOggOpus, audio.Format)
	}
	if string(audio.Data) != string(wantAudio) {
		t.Fatalf("expected audio %q, got %q", wantAudio, audio.Data)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if strings.Contains(gotURL, apiKey) {
		t.Fatalf("API key must never appear in request URL: %s", gotURL)
	}
	if gotKeyHdr != apiKey {
		t.Fatalf("expected X-Goog-Api-Key %q, got %q", apiKey, gotKeyHdr)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("expected application/json content-type, got %q", gotCT)
	}
	wantReq := googleRequest{
		Input: googleInput{Text: "สวัสดีครับ"},
		Voice: googleVoice{
			LanguageCode: "th-TH",
			Name:         DefaultVoiceTH,
		},
		AudioConfig: googleAudioConfig{
			AudioEncoding:   "OGG_OPUS",
			SampleRateHertz: 48000,
		},
	}
	if gotReq != wantReq {
		t.Fatalf("expected request %+v, got %+v", wantReq, gotReq)
	}
}

func TestGoogle_VoiceMappingAndOverride(t *testing.T) {
	tests := []struct {
		name      string
		lang      string
		override  string
		wantLang  string
		wantVoice string
	}{
		{"default thai", "th", "", "th-TH", DefaultVoiceTH},
		{"default english", "en", "", "en-US", DefaultVoiceEN},
		{"thai chirp3 override", "th", "th-TH-Chirp3-HD-Achird", "th-TH", "th-TH-Chirp3-HD-Achird"},
		{"bare speaker maps to thai chirp3", "th", "Puck", "th-TH", "th-TH-Chirp3-HD-Puck"},
		{"bare speaker maps to english chirp3", "en", "Orus", "en-US", "en-US-Chirp3-HD-Orus"},
		{"english voice override with th lang", "th", "en-US-Neural2-D", "en-US", "en-US-Neural2-D"},
		{"other bcp47 lang without default voice", "fr-FR", "", "fr-FR", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotVoice googleVoice
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req googleRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode: %v", err)
				}
				gotVoice = req.Voice
				_, _ = w.Write([]byte(`{"audioContent":"T2dnUw=="}`))
			}))
			defer srv.Close()

			g := &Google{
				APIKey:   "test-key",
				Voice:    tt.override,
				Endpoint: srv.URL,
				Client:   srv.Client(),
			}
			if _, err := g.Synthesize(context.Background(), "hello", tt.lang); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotVoice.LanguageCode != tt.wantLang || gotVoice.Name != tt.wantVoice {
				t.Fatalf("expected voice (%q, %q), got (%q, %q)",
					tt.wantLang, tt.wantVoice, gotVoice.LanguageCode, gotVoice.Name)
			}
		})
	}
}

func TestGoogle_MissingAPIKey(t *testing.T) {
	g := &Google{}
	if _, err := g.Synthesize(context.Background(), "hi", "en"); err == nil {
		t.Fatal("expected error when APIKey is empty, got nil")
	}
}

func TestGoogle_HTTPErrorIncludesStatusAndGoogleMessageWithoutKey(t *testing.T) {
	const apiKey = "super-secret-api-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{
			"error": {
				"code": 403,
				"message": "Cloud Text-to-Speech API has not been used in project (key super-secret-api-key)",
				"status": "PERMISSION_DENIED"
			}
		}`))
	}))
	defer srv.Close()

	g := &Google{APIKey: apiKey, Endpoint: srv.URL, Client: srv.Client()}
	_, err := g.Synthesize(context.Background(), "hi", "en")
	if err == nil {
		t.Fatal("expected error on HTTP 403, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") || !strings.Contains(msg, "PERMISSION_DENIED") || !strings.Contains(msg, "Cloud Text-to-Speech API") {
		t.Fatalf("expected HTTP status and Google error message, got %q", msg)
	}
	if strings.Contains(msg, apiKey) {
		t.Fatalf("error message leaked API key: %q", msg)
	}
}

func TestGoogle_EmptyAudioContentErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"audioContent":""}`))
	}))
	defer srv.Close()

	g := &Google{APIKey: "k", Endpoint: srv.URL, Client: srv.Client()}
	if _, err := g.Synthesize(context.Background(), "hi", "en"); err == nil {
		t.Fatal("expected error for empty audioContent, got nil")
	}
}

func TestGoogle_RespectsContextTimeout(t *testing.T) {
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer srv.Close()
	defer close(unblock)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	g := &Google{APIKey: "k", Endpoint: srv.URL, Client: srv.Client()}
	if _, err := g.Synthesize(ctx, "hi", "en"); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// captureBody serves a canned response and records the raw request body.
func captureBody(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"audioContent":"T2dnUw=="}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

func TestGoogle_GeminiRequestJSON(t *testing.T) {
	srv, body := captureBody(t)
	g := &Google{
		APIKey: "k", Endpoint: srv.URL, Client: srv.Client(),
		Model: "gemini-2.5-flash-tts", Voice: "Charon", Prompt: "พูดแบบเป็นกันเอง",
	}
	if _, err := g.Synthesize(context.Background(), "สวัสดี", "th"); err != nil {
		t.Fatal(err)
	}
	want := `{"input":{"text":"สวัสดี","prompt":"พูดแบบเป็นกันเอง"},` +
		`"voice":{"languageCode":"th-TH","name":"Charon","modelName":"gemini-2.5-flash-tts"},` +
		`"audioConfig":{"audioEncoding":"OGG_OPUS","sampleRateHertz":48000}}`
	if *body != want {
		t.Fatalf("request JSON\n got: %s\nwant: %s", *body, want)
	}
}

func TestGoogle_GeminiDefaultsAndLanguage(t *testing.T) {
	srv, body := captureBody(t)
	g := &Google{APIKey: "k", Endpoint: srv.URL, Client: srv.Client(), Model: "gemini-2.5-flash-tts"}
	if _, err := g.Synthesize(context.Background(), "hello", "en"); err != nil {
		t.Fatal(err)
	}
	want := `{"input":{"text":"hello"},` +
		`"voice":{"languageCode":"en-US","name":"Charon","modelName":"gemini-2.5-flash-tts"},` +
		`"audioConfig":{"audioEncoding":"OGG_OPUS","sampleRateHertz":48000}}`
	if *body != want {
		t.Fatalf("request JSON\n got: %s\nwant: %s", *body, want)
	}
}

func TestGoogle_ChirpNeverGetsPromptOrModelName(t *testing.T) {
	srv, body := captureBody(t)
	// A stray prompt must not leak into a non-Gemini request.
	g := &Google{
		APIKey: "k", Endpoint: srv.URL, Client: srv.Client(),
		Voice: "th-TH-Chirp3-HD-Achird", Prompt: "should be ignored",
	}
	if _, err := g.Synthesize(context.Background(), "สวัสดี", "th"); err != nil {
		t.Fatal(err)
	}
	want := `{"input":{"text":"สวัสดี"},` +
		`"voice":{"languageCode":"th-TH","name":"th-TH-Chirp3-HD-Achird"},` +
		`"audioConfig":{"audioEncoding":"OGG_OPUS","sampleRateHertz":48000}}`
	if *body != want {
		t.Fatalf("request JSON\n got: %s\nwant: %s", *body, want)
	}
	if strings.Contains(*body, "prompt") || strings.Contains(*body, "modelName") {
		t.Fatalf("non-Gemini request carries Gemini fields: %s", *body)
	}
}

func TestIsGeminiModel(t *testing.T) {
	for model, want := range map[string]bool{
		"gemini-2.5-flash-tts": true, " Gemini-2.5-pro-tts ": true, "": false, "chirp3": false,
	} {
		if got := IsGeminiModel(model); got != want {
			t.Errorf("IsGeminiModel(%q) = %v, want %v", model, got, want)
		}
	}
}

// With no API key, an authenticated client (OAuth2 for a service account) is
// used as is and no X-Goog-Api-Key header is sent.
func TestGoogle_ClientWithoutAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := r.Header.Get("X-Goog-Api-Key"); k != "" {
			t.Errorf("unexpected X-Goog-Api-Key header %q", k)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"audioContent": base64.StdEncoding.EncodeToString([]byte("OggS")),
		})
	}))
	defer srv.Close()

	g := &Google{Endpoint: srv.URL, Client: srv.Client(), Model: "gemini-2.5-flash-tts"}
	if _, err := g.Synthesize(context.Background(), "สวัสดี", "th"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if _, err := (&Google{Endpoint: srv.URL}).Synthesize(context.Background(), "x", "th"); err == nil {
		t.Fatal("want an error with neither API key nor client")
	}
}

func TestGoogle_PerMessageVoiceOverridesDefaultAndFallsBack(t *testing.T) {
	t.Run("gemini model", func(t *testing.T) {
		srv, body := captureBody(t)
		g := &Google{
			APIKey: "k", Endpoint: srv.URL, Client: srv.Client(),
			Model: "gemini-2.5-flash-tts", Voice: "Charon",
		}
		ctx := WithVoice(context.Background(), "Puck")
		if _, err := g.Synthesize(ctx, "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*body, `"name":"Puck"`) {
			t.Fatalf("expected per-message voice Puck, got %s", *body)
		}
		// Without WithVoice it falls back to g.Voice (Charon).
		if _, err := g.Synthesize(context.Background(), "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*body, `"name":"Charon"`) {
			t.Fatalf("expected fallback voice Charon, got %s", *body)
		}
	})

	t.Run("classic chirp3 mapping", func(t *testing.T) {
		srv, body := captureBody(t)
		g := &Google{
			APIKey: "k", Endpoint: srv.URL, Client: srv.Client(),
			Voice: "th-TH-Chirp3-HD-Charon",
		}
		ctx := WithVoice(context.Background(), "Fenrir")
		if _, err := g.Synthesize(ctx, "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*body, `"name":"th-TH-Chirp3-HD-Fenrir"`) {
			t.Fatalf("expected mapped Chirp3-HD voice, got %s", *body)
		}
		// Without WithVoice it falls back to g.Voice.
		if _, err := g.Synthesize(context.Background(), "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*body, `"name":"th-TH-Chirp3-HD-Charon"`) {
			t.Fatalf("expected fallback voice, got %s", *body)
		}
	})
}
