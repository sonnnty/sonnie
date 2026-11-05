package config

import (
	"fmt"
	"net/url"
	"strings"
)

// DashboardConfig holds the settings of the built-in HTTP server: the
// monitoring dashboard behind Discord login, /healthz and the webhooks.
type DashboardConfig struct {
	// Addr (DASHBOARD_ADDR) is the listen address. It defaults to ":8080";
	// set to "" or "off" it turns the HTTP server off altogether.
	Addr string
	// ClientID (DISCORD_CLIENT_ID) is the OAuth2 client ID. Empty means the
	// bot's application ID from the gateway Ready event.
	ClientID string
	// ClientSecret (DISCORD_CLIENT_SECRET, a secret) is the OAuth2 client
	// secret from the Developer Portal.
	ClientSecret string
	// PublicURL (DASHBOARD_PUBLIC_URL) is where browsers reach the server,
	// without a trailing slash; the redirect URI is PublicURL+"/auth/callback".
	PublicURL string
	// SessionKey (DASHBOARD_SESSION_KEY, a secret, at least 32 bytes) seals
	// the session cookie.
	SessionKey []byte
	// AllowedUsers (DASHBOARD_ALLOWED_USERS) may always see the dashboard,
	// on top of the guild administrators and managers.
	AllowedUsers []string
	// GuildID (DASHBOARD_GUILD_ID) limits login to members of this server.
	// Empty means any server the bot is in.
	GuildID string
	// RoleIDs (DASHBOARD_ROLE_IDS) lets in members of GuildID who have at
	// least one of these roles, instead of requiring Administrator or Manage
	// Server. Needs GuildID.
	RoleIDs []string
}

// LoginEnabled reports whether Discord login, and with it the dashboard
// pages, are configured. /healthz and the webhooks work without it.
func (d DashboardConfig) LoginEnabled() bool {
	return d.ClientSecret != "" && d.PublicURL != "" && len(d.SessionKey) > 0
}

// RedirectURI is the OAuth2 redirect URI to register in the Developer Portal.
func (d DashboardConfig) RedirectURI() string {
	return d.PublicURL + "/auth/callback"
}

// WebhookConfig is one entry of WEBHOOKS: POST /hooks/<Name> with Token posts
// to ChannelID.
type WebhookConfig struct {
	Name      string
	Token     string
	ChannelID string
}

const (
	defaultDashboardAddr = ":8080"
	// minSessionKeyLen is the least DASHBOARD_SESSION_KEY length in bytes.
	minSessionKeyLen = 32
	// minWebhookTokenLen keeps webhook tokens out of guessing range.
	minWebhookTokenLen = 16
	maxWebhookNameLen  = 32
)

func loadDashboard(lookup func(string) (string, bool)) (DashboardConfig, error) {
	get := func(key string) string {
		v, _ := lookup(key)
		return strings.TrimSpace(v)
	}
	cfg := DashboardConfig{Addr: defaultDashboardAddr}
	if v, ok := lookup("DASHBOARD_ADDR"); ok {
		cfg.Addr = strings.TrimSpace(v)
		if strings.EqualFold(cfg.Addr, "off") {
			cfg.Addr = ""
		}
	}

	if cfg.ClientID = get("DISCORD_CLIENT_ID"); cfg.ClientID != "" && !isSnowflake(cfg.ClientID) {
		return DashboardConfig{}, fmt.Errorf("DISCORD_CLIENT_ID: %q is not a Discord application ID", cfg.ClientID)
	}
	cfg.ClientSecret = get("DISCORD_CLIENT_SECRET")
	if key := get("DASHBOARD_SESSION_KEY"); key != "" {
		if len(key) < minSessionKeyLen {
			// The key itself is never part of an error message.
			return DashboardConfig{}, fmt.Errorf("DASHBOARD_SESSION_KEY: must be at least %d bytes, got %d (generate one with: openssl rand -base64 48)", minSessionKeyLen, len(key))
		}
		cfg.SessionKey = []byte(key)
	}
	if raw := get("DASHBOARD_PUBLIC_URL"); raw != "" {
		u, err := parsePublicURL(raw)
		if err != nil {
			return DashboardConfig{}, err
		}
		cfg.PublicURL = u
	}
	for _, part := range strings.Split(get("DASHBOARD_ALLOWED_USERS"), ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if !isSnowflake(id) {
			return DashboardConfig{}, fmt.Errorf("DASHBOARD_ALLOWED_USERS: %q is not a Discord user ID", id)
		}
		cfg.AllowedUsers = append(cfg.AllowedUsers, id)
	}

	if g := strings.TrimSpace(get("DASHBOARD_GUILD_ID")); g != "" {
		if !isSnowflake(g) {
			return DashboardConfig{}, fmt.Errorf("DASHBOARD_GUILD_ID: %q is not a Discord server ID", g)
		}
		cfg.GuildID = g
	}
	for _, part := range strings.Split(get("DASHBOARD_ROLE_IDS"), ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if !isSnowflake(id) {
			return DashboardConfig{}, fmt.Errorf("DASHBOARD_ROLE_IDS: %q is not a Discord role ID", id)
		}
		cfg.RoleIDs = append(cfg.RoleIDs, id)
	}
	if len(cfg.RoleIDs) > 0 && cfg.GuildID == "" {
		return DashboardConfig{}, fmt.Errorf("DASHBOARD_ROLE_IDS needs DASHBOARD_GUILD_ID (the server the roles belong to)")
	}

	// Login needs all three; a partial set is a mistake worth stopping for
	// rather than a dashboard that silently cannot log anyone in.
	var missing []string
	if cfg.ClientSecret == "" {
		missing = append(missing, "DISCORD_CLIENT_SECRET")
	}
	if cfg.PublicURL == "" {
		missing = append(missing, "DASHBOARD_PUBLIC_URL")
	}
	if len(cfg.SessionKey) == 0 {
		missing = append(missing, "DASHBOARD_SESSION_KEY")
	}
	if len(missing) > 0 && len(missing) < 3 {
		return DashboardConfig{}, fmt.Errorf("dashboard login needs DISCORD_CLIENT_SECRET, DASHBOARD_PUBLIC_URL and DASHBOARD_SESSION_KEY together; missing %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// parsePublicURL accepts an absolute http(s) URL without query or fragment
// and returns it without a trailing slash.
func parsePublicURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("DASHBOARD_PUBLIC_URL: %q is not an http(s) URL such as http://10.10.5.35:31120", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// parseWebhooks reads WEBHOOKS: "name=token@channelID" entries separated by
// commas. Errors name the entry but never repeat a token.
func parseWebhooks(raw string) ([]WebhookConfig, error) {
	var hooks []WebhookConfig
	seen := map[string]bool{}
	for i, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rest, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || !validWebhookName(name) {
			return nil, fmt.Errorf("WEBHOOKS: entry %d: expected name=token@channelID with a name of lowercase letters, digits, - or _ (at most %d)", i+1, maxWebhookNameLen)
		}
		at := strings.LastIndex(rest, "@")
		if at < 0 {
			return nil, fmt.Errorf("WEBHOOKS: %s: expected name=token@channelID", name)
		}
		token, channel := strings.TrimSpace(rest[:at]), strings.TrimSpace(rest[at+1:])
		if len(token) < minWebhookTokenLen || strings.ContainsAny(token, " \t\r\n") {
			return nil, fmt.Errorf("WEBHOOKS: %s: the token must be at least %d characters without spaces (generate one with: openssl rand -hex 24)", name, minWebhookTokenLen)
		}
		if !isSnowflake(channel) {
			return nil, fmt.Errorf("WEBHOOKS: %s: %q is not a Discord channel ID", name, channel)
		}
		if seen[name] {
			return nil, fmt.Errorf("WEBHOOKS: %s is listed twice", name)
		}
		seen[name] = true
		hooks = append(hooks, WebhookConfig{Name: name, Token: token, ChannelID: channel})
	}
	return hooks, nil
}

func validWebhookName(s string) bool {
	if s == "" || len(s) > maxWebhookNameLen {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
