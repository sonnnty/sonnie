package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultGoogleEndpoint is the Google Cloud Text-to-Speech v1 REST endpoint.
	DefaultGoogleEndpoint = "https://texttospeech.googleapis.com/v1/text:synthesize"

	defaultGoogleTimeout  = 15 * time.Second
	googleSampleRateHertz = 48000
	googleAudioEncoding   = "OGG_OPUS"
	maxErrorBodyBytes     = 64 << 10

	// DefaultVoiceTH is the default Thai voice (Neural2 female).
	DefaultVoiceTH = "th-TH-Neural2-C"
	// DefaultVoiceEN is the default English voice (Neural2 female).
	DefaultVoiceEN = "en-US-Neural2-F"
)

var defaultGoogleClient = &http.Client{Timeout: defaultGoogleTimeout}

// Google synthesizes speech with the Google Cloud Text-to-Speech v1 REST API,
// requesting 48 kHz Ogg/Opus directly so the voice package only needs to demux
// Ogg pages into Opus frames.
type Google struct {
	// APIKey is sent in the X-Goog-Api-Key header and is never placed in
	// URLs or error messages.
	APIKey string
	// Voice, if non-empty, overrides the default voice name selected from
	// the language code (e.g. "th-TH-Chirp3-HD-Achird").
	Voice string
	// Endpoint overrides DefaultGoogleEndpoint; intended for tests.
	Endpoint string
	// Client is the HTTP client used for requests. If nil, a client with a
	// 15 s timeout is used.
	Client *http.Client
}

type googleRequest struct {
	Input       googleInput       `json:"input"`
	Voice       googleVoice       `json:"voice"`
	AudioConfig googleAudioConfig `json:"audioConfig"`
}

type googleInput struct {
	Text string `json:"text"`
}

type googleVoice struct {
	LanguageCode string `json:"languageCode"`
	Name         string `json:"name,omitempty"`
}

type googleAudioConfig struct {
	AudioEncoding   string `json:"audioEncoding"`
	SampleRateHertz int    `json:"sampleRateHertz"`
}

type googleResponse struct {
	AudioContent []byte `json:"audioContent"`
}

type googleErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// Synthesize calls Google Cloud Text-to-Speech and returns FormatOggOpus audio
// at 48 kHz.
func (g *Google) Synthesize(ctx context.Context, text, lang string) (Audio, error) {
	if g.APIKey == "" {
		return Audio{}, errors.New("tts: google: API key is not set")
	}

	langCode, voiceName := resolveVoice(lang, g.Voice)
	payload, err := json.Marshal(googleRequest{
		Input: googleInput{Text: text},
		Voice: googleVoice{
			LanguageCode: langCode,
			Name:         voiceName,
		},
		AudioConfig: googleAudioConfig{
			AudioEncoding:   googleAudioEncoding,
			SampleRateHertz: googleSampleRateHertz,
		},
	})
	if err != nil {
		return Audio{}, fmt.Errorf("tts: google: marshal request: %w", err)
	}

	endpoint := g.Endpoint
	if endpoint == "" {
		endpoint = DefaultGoogleEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Audio{}, fmt.Errorf("tts: google: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Goog-Api-Key", g.APIKey)

	client := g.Client
	if client == nil {
		client = defaultGoogleClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Audio{}, fmt.Errorf("tts: google: %s", redactKey(err.Error(), g.APIKey))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return Audio{}, formatGoogleError(resp.StatusCode, body, g.APIKey)
	}

	var out googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Audio{}, fmt.Errorf("tts: google: decode response: %w", err)
	}
	if len(out.AudioContent) == 0 {
		return Audio{}, errors.New("tts: google: empty audioContent in response")
	}
	return Audio{Format: FormatOggOpus, Data: out.AudioContent}, nil
}

// resolveVoice maps a short or BCP 47 language code to a Google languageCode
// and default voice name, applying voiceOverride when set.
func resolveVoice(lang, voiceOverride string) (langCode, voiceName string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "th", "th-th":
		langCode, voiceName = "th-TH", DefaultVoiceTH
	case "en", "en-us":
		langCode, voiceName = "en-US", DefaultVoiceEN
	case "en-gb":
		langCode, voiceName = "en-GB", "en-GB-Neural2-A"
	case "ja", "ja-jp":
		langCode, voiceName = "ja-JP", "ja-JP-Neural2-B"
	default:
		langCode = strings.TrimSpace(lang)
	}

	if v := strings.TrimSpace(voiceOverride); v != "" {
		voiceName = v
		if prefix := voiceLangPrefix(v); prefix != "" {
			langCode = prefix
		}
	}
	return langCode, voiceName
}

// voiceLangPrefix extracts a leading "<lang>-<REGION>" prefix from a Google
// voice name such as "th-TH-Chirp3-HD-Achird" or "en-US-Neural2-D". It returns
// "" if name does not start with that pattern.
func voiceLangPrefix(name string) string {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 3 {
		return ""
	}
	lang, region := parts[0], parts[1]
	if len(lang) < 2 || len(lang) > 3 || len(region) != 2 {
		return ""
	}
	for i := 0; i < len(lang); i++ {
		if lang[i] < 'a' || lang[i] > 'z' {
			return ""
		}
	}
	for i := 0; i < len(region); i++ {
		if region[i] < 'A' || region[i] > 'Z' {
			return ""
		}
	}
	return lang + "-" + region
}

func formatGoogleError(statusCode int, body []byte, apiKey string) error {
	var parsed googleErrorResponse
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		msg := redactKey(parsed.Error.Message, apiKey)
		if parsed.Error.Status != "" {
			return fmt.Errorf("tts: google: HTTP %d (%s): %s", statusCode, parsed.Error.Status, msg)
		}
		return fmt.Errorf("tts: google: HTTP %d: %s", statusCode, msg)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(statusCode)
	}
	return fmt.Errorf("tts: google: HTTP %d: %s", statusCode, redactKey(msg, apiKey))
}

func redactKey(s, apiKey string) string {
	if apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "[REDACTED]")
}
