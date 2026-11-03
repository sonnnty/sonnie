package cover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"unicode"
)

// Keyworder turns an event name into English words for a photo search.
type Keyworder interface {
	Keywords(ctx context.Context, name string) (string, error)
}

var (
	// ErrRejected means Google refused the call (no permission). Later calls
	// return ErrDisabled without asking again.
	ErrRejected = errors.New("gemini: request rejected")
	ErrDisabled = errors.New("gemini: disabled after a rejected request")
)

// DefaultGeminiModel is cheap and fast; thinking is off by default for it.
const DefaultGeminiModel = "gemini-2.5-flash-lite"

// Gemini asks a Gemini model on Vertex AI (generateContent) for keywords,
// authenticating with the service account that also drives Gemini-TTS.
type Gemini struct {
	// Client must add OAuth credentials (cloud-platform scope).
	Client   *http.Client
	Project  string
	Location string // default "global"
	Model    string // default DefaultGeminiModel
	// Endpoint overrides the Vertex AI host, for tests.
	Endpoint string

	rejected atomic.Bool
}

func (g *Gemini) url() string {
	loc := g.Location
	if loc == "" {
		loc = "global"
	}
	model := g.Model
	if model == "" {
		model = DefaultGeminiModel
	}
	host := g.Endpoint
	switch {
	case host != "":
	case loc == "global":
		host = "https://aiplatform.googleapis.com"
	default:
		host = "https://" + loc + "-aiplatform.googleapis.com"
	}
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", host, g.Project, loc, model)
}

const keywordPrompt = `You pick search words for a stock photo that will be the cover image of a Discord community event.
Event name: %q
Reply with 2 to 4 English keywords for the photo search, separated by spaces, and nothing else.`

type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig map[string]any  `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

func (g *Gemini) Keywords(ctx context.Context, name string) (string, error) {
	if g.rejected.Load() {
		return "", ErrDisabled
	}
	body, err := json.Marshal(geminiRequest{
		Contents:         []geminiContent{{Role: "user", Parts: []geminiPart{{Text: fmt.Sprintf(keywordPrompt, name)}}}},
		GenerationConfig: map[string]any{"temperature": 0.2, "maxOutputTokens": 40},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		g.rejected.Store(true)
		return "", fmt.Errorf("%w (HTTP %d)", ErrRejected, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out geminiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("gemini: %w", err)
	}
	var text strings.Builder
	for _, c := range out.Candidates {
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
			text.WriteString(" ")
		}
		break
	}
	kw := CleanKeywords(text.String())
	if kw == "" {
		return "", errors.New("gemini: no keywords in the reply")
	}
	return kw, nil
}

// maxKeywords keeps a chatty reply from turning into a long query.
const maxKeywords = 4

// CleanKeywords keeps up to four lower-case ASCII words.
func CleanKeywords(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-'))
	})
	if len(words) > maxKeywords {
		words = words[:maxKeywords]
	}
	return strings.Join(words, " ")
}

// HasThai reports whether s contains Thai letters, which the photo
// libraries search poorly.
func HasThai(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Thai, r) {
			return true
		}
	}
	return false
}
