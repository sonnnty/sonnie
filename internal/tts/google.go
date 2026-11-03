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
	// DefaultGeminiSpeaker is the speaker used with a Gemini model when no
	// voice is configured.
	DefaultGeminiSpeaker = "Charon"
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
	// Model, if it names a Gemini-TTS model (e.g. "gemini-2.5-flash-tts"),
	// switches the request to the Gemini shape: Voice is then the bare
	// speaker name (default DefaultGeminiSpeaker) and voice.modelName is
	// set. Empty keeps the Chirp/Neural2 behaviour.
	Model string
	// Prompt is a natural-language style instruction sent as input.prompt.
	// It is only sent for Gemini models; other voices reject it.
	Prompt string
	// Endpoint overrides DefaultGoogleEndpoint; intended for tests.
	Endpoint string
	// Client is the HTTP client used for requests. If nil, a client with a
	// 15 s timeout is used. With APIKey empty, Client must authenticate the
	// requests itself (e.g. an OAuth2 client from NewGoogleOAuthClient).
	Client *http.Client
}

type googleRequest struct {
	Input       googleInput       `json:"input"`
	Voice       googleVoice       `json:"voice"`
	AudioConfig googleAudioConfig `json:"audioConfig"`
}

type googleInput struct {
	Text   string `json:"text"`
	Prompt string `json:"prompt,omitempty"`
}

type googleVoice struct {
	LanguageCode string `json:"languageCode"`
	Name         string `json:"name,omitempty"`
	ModelName    string `json:"modelName,omitempty"`
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
	if g.APIKey == "" && g.Client == nil {
		return Audio{}, errors.New("tts: google: neither an API key nor an authenticated client is set")
	}

	payload, err := json.Marshal(g.buildRequest(text, lang))
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
	if g.APIKey != "" {
		req.Header.Set("X-Goog-Api-Key", g.APIKey)
	}

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

// IsGeminiModel reports whether model names a Gemini-TTS model.
func IsGeminiModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gemini")
}

func (g *Google) buildRequest(text, lang string) googleRequest {
	req := googleRequest{
		Input: googleInput{Text: text},
		AudioConfig: googleAudioConfig{
			AudioEncoding:   googleAudioEncoding,
			SampleRateHertz: googleSampleRateHertz,
		},
	}
	if IsGeminiModel(g.Model) {
		speaker := strings.TrimSpace(g.Voice)
		if speaker == "" {
			speaker = DefaultGeminiSpeaker
		}
		req.Input.Prompt = strings.TrimSpace(g.Prompt)
		req.Voice = googleVoice{
			LanguageCode: languageCode(lang),
			Name:         speaker,
			ModelName:    strings.TrimSpace(g.Model),
		}
		return req
	}
	langCode, voiceName := resolveVoice(lang, g.Voice)
	req.Voice = googleVoice{LanguageCode: langCode, Name: voiceName}
	return req
}

// languageCode maps a short or BCP 47 language code to a Google languageCode.
func languageCode(lang string) string {
	code, _ := resolveVoice(lang, "")
	return code
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
