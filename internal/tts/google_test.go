package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
