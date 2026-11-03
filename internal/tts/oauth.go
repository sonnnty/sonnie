package tts

import (
	"context"
	"fmt"
	"net/http"
	"os"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// LoadGoogleCredentials reads the service account JSON key file at path.
// Gemini-TTS models need a service account: the Text-to-Speech endpoint
// rejects API keys for them.
func LoadGoogleCredentials(ctx context.Context, path string) (*google.Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tts: google: read credentials: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("tts: google: parse credentials: %w", err)
	}
	return creds, nil
}

// NewGoogleOAuthClient returns an HTTP client for the REST API that
// authenticates as the service account in the JSON key file at path.
func NewGoogleOAuthClient(ctx context.Context, path string) (*http.Client, error) {
	creds, err := LoadGoogleCredentials(ctx, path)
	if err != nil {
		return nil, err
	}
	return NewGoogleOAuthClientFromCredentials(ctx, creds), nil
}

// NewGoogleOAuthClientFromCredentials returns an HTTP client for the REST API
// that authenticates with creds.
func NewGoogleOAuthClientFromCredentials(ctx context.Context, creds *google.Credentials) *http.Client {
	client := oauth2.NewClient(ctx, creds.TokenSource)
	client.Timeout = defaultGoogleTimeout
	return client
}

// NewGoogleStreamOpener connects a gRPC Text-to-Speech client that
// authenticates with the same token source as the REST client and returns
// its StreamingSynthesize call. The connection is made lazily and lives for
// the rest of the process; closeFn is for tests and orderly shutdown.
func NewGoogleStreamOpener(ctx context.Context, creds *google.Credentials) (open StreamOpener, closeFn func() error, err error) {
	client, err := texttospeech.NewClient(ctx, option.WithTokenSource(creds.TokenSource))
	if err != nil {
		return nil, nil, fmt.Errorf("tts: google: grpc client: %w", err)
	}
	open = func(ctx context.Context) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error) {
		return client.StreamingSynthesize(ctx)
	}
	return open, client.Close, nil
}
