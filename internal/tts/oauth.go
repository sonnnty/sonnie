package tts

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// NewGoogleOAuthClient returns an HTTP client that authenticates as the
// service account in the JSON key file at path. Gemini-TTS models need this:
// the Text-to-Speech endpoint rejects API keys for them.
func NewGoogleOAuthClient(ctx context.Context, path string) (*http.Client, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tts: google: read credentials: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("tts: google: parse credentials: %w", err)
	}
	client := oauth2.NewClient(ctx, creds.TokenSource)
	client.Timeout = defaultGoogleTimeout
	return client, nil
}
