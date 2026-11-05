package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoints are Discord's OAuth2 URLs; tests point them at a fake.
type Endpoints struct {
	AuthorizeURL string
	TokenURL     string
	// UserURL is GET /users/@me.
	UserURL string
}

// DiscordEndpoints are the real ones.
var DiscordEndpoints = Endpoints{
	AuthorizeURL: "https://discord.com/oauth2/authorize",
	TokenURL:     "https://discord.com/api/v10/oauth2/token",
	UserURL:      "https://discord.com/api/v10/users/@me",
}

// oauthScope is all the dashboard asks for: who the user is. Guild access is
// checked with the bot's own credentials, not the user's token.
const oauthScope = "identify"

const oauthTimeout = 10 * time.Second

// DiscordUser is the part of /users/@me the dashboard uses.
type DiscordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

// DisplayName is the global display name, else the username.
func (u DiscordUser) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

type oauthClient struct {
	endpoints    Endpoints
	clientID     func() string
	clientSecret string
	redirectURI  string
	http         *http.Client
}

func (c *oauthClient) authorizeURL(state string) string {
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {c.clientID()},
		"scope":         {oauthScope},
		"redirect_uri":  {c.redirectURI},
		"state":         {state},
		// Skip the consent screen for users who already authorised the app.
		"prompt": {"none"},
	}
	return c.endpoints.AuthorizeURL + "?" + q.Encode()
}

// exchange trades the authorization code for an access token, server side.
func (c *oauthClient) exchange(ctx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(c.clientID()), url.QueryEscape(c.clientSecret))
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if err := c.doJSON(req, &tok); err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token exchange: no access token (%s)", tok.Error)
	}
	return tok.AccessToken, nil
}

// user fetches /users/@me with the user's access token.
func (c *oauthClient) user(ctx context.Context, accessToken string) (DiscordUser, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.UserURL, nil)
	if err != nil {
		return DiscordUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	var u DiscordUser
	if err := c.doJSON(req, &u); err != nil {
		return DiscordUser{}, fmt.Errorf("users/@me: %w", err)
	}
	if u.ID == "" {
		return DiscordUser{}, fmt.Errorf("users/@me: no user ID")
	}
	return u, nil
}

// doJSON runs req and decodes a 2xx JSON answer into v. Error answers are
// reported by status and Discord's error code only: their bodies are never
// logged in full.
func (c *oauthClient) doJSON(req *http.Request, v any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, e.Error)
	}
	return json.Unmarshal(body, v)
}
