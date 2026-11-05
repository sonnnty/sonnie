package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/config"
	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/panels"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/webhook"
)

// dashboardShutdownTimeout bounds the wait for requests in flight on exit.
const dashboardShutdownTimeout = 5 * time.Second

// newDashboard builds the HTTP server (dashboard, /healthz, webhooks), or
// returns nil when DASHBOARD_ADDR is empty.
//
// Every feature shows itself as one panel. Adding a feature: write its panel
// in internal/panels and append it in dashboardPanels below.
func (b *Bot) newDashboard(cfg config.Config) (*dashboard.Server, error) {
	if len(cfg.Webhooks) > 0 {
		hooks := make([]webhook.Hook, len(cfg.Webhooks))
		for i, h := range cfg.Webhooks {
			hooks[i] = webhook.Hook{Name: h.Name, Token: h.Token, ChannelID: h.ChannelID}
		}
		b.hooks = webhook.New(webhook.Config{Hooks: hooks, Poster: b.Session})
	}
	b.redactSecrets(cfg)

	if cfg.Dashboard.Addr == "" {
		log.Println("dashboard: DASHBOARD_ADDR is empty; the HTTP server (dashboard, /healthz, webhooks) is off")
		if b.hooks != nil {
			log.Println("dashboard: WEBHOOKS is set but cannot be called without the HTTP server")
		}
		return nil, nil
	}

	d := cfg.Dashboard
	srvCfg := dashboard.Config{
		Addr:         d.Addr,
		PublicURL:    d.PublicURL,
		ClientSecret: d.ClientSecret,
		SessionKey:   d.SessionKey,
		ClientID: func() string {
			if d.ClientID != "" {
				return d.ClientID
			}
			return b.applicationID()
		},
		Access: dashboard.GuildAccess{
			Allowed: d.AllowedUsers,
			Guilds:  stateGuilds{s: b.Session},
			GuildID: d.GuildID,
			RoleIDs: d.RoleIDs,
		},
		Panels:  b.dashboardPanels(),
		Healthy: b.connected.Load,
	}
	if b.hooks != nil {
		srvCfg.Webhooks = b.hooks
	}
	srv, err := dashboard.New(srvCfg)
	if err != nil {
		return nil, err
	}
	if srv.LoginEnabled() {
		who := "Administrator or Manage Server in any server the bot is in"
		switch {
		case len(d.RoleIDs) > 0:
			who = fmt.Sprintf("members of server %s with role %s", d.GuildID, strings.Join(d.RoleIDs, " or "))
		case d.GuildID != "":
			who = "Administrator or Manage Server in server " + d.GuildID
		}
		log.Printf("dashboard: login allowed for %s, plus %d listed user(s)", who, len(d.AllowedUsers))
		log.Printf("dashboard: listening on %s; Discord login with redirect URI %s (register it in the Developer Portal → OAuth2 → Redirects); %d hook(s)",
			d.Addr, d.RedirectURI(), len(cfg.Webhooks))
	} else {
		log.Printf("dashboard: listening on %s for /healthz and %d hook(s); the dashboard needs DISCORD_CLIENT_SECRET, DASHBOARD_PUBLIC_URL and DASHBOARD_SESSION_KEY",
			d.Addr, len(cfg.Webhooks))
	}
	return srv, nil
}

func (b *Bot) dashboardPanels() []dashboard.Panel {
	names := stateNames{s: b.Session}

	// Typed nils would look like enabled features; pass plain nil.
	var sessions panels.SessionSource
	if b.readAloud != nil {
		sessions = b.readAloud
	}
	var meter panels.UsageSource
	if b.meter != nil {
		meter = b.meter
	}
	var hooks panels.WebhookSource
	if b.hooks != nil {
		hooks = b.hooks
	}

	list := []dashboard.Panel{
		panels.Bot(b.started, b.botInfo, nil),
		panels.TTSSessions(sessions, names),
		panels.TTSUsage(meter, tts.RecentTimings, nil),
		panels.Events(b.eventSched, b.covers, names),
		panels.Webhooks(hooks, names),
	}
	if b.logs != nil {
		list = append(list, panels.Logs(b.logs))
	}
	return list
}

// redactSecrets keeps every configured secret out of the dashboard's log
// view, should anything ever log one.
func (b *Bot) redactSecrets(cfg config.Config) {
	if b.logs == nil {
		return
	}
	secrets := []string{
		cfg.DiscordToken, cfg.Dashboard.ClientSecret, string(cfg.Dashboard.SessionKey),
		cfg.TTS.GoogleAPIKey, cfg.Events.BraveKey, cfg.Events.UnsplashKey, cfg.Events.PexelsKey,
	}
	if b.hooks != nil {
		secrets = append(secrets, b.hooks.Tokens()...)
	}
	b.logs.SetSecrets(secrets...)
}

// startDashboard starts listening before the gateway connects, so /healthz
// answers 503 rather than refusing connections while the bot starts.
func (b *Bot) startDashboard() error {
	if b.dash == nil {
		return nil
	}
	return b.dash.Start()
}

func (b *Bot) stopDashboard() {
	if b.dash == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dashboardShutdownTimeout)
	defer cancel()
	if err := b.dash.Shutdown(ctx); err != nil {
		log.Printf("dashboard: shutdown: %v", err)
	}
}

// Gateway state for /healthz and the Bot panel.

func (b *Bot) handleReady(_ *discordgo.Session, r *discordgo.Ready) {
	if r.Application != nil && r.Application.ID != "" {
		b.appID.Store(r.Application.ID)
	}
	b.connected.Store(true)
}

func (b *Bot) handleResumed(*discordgo.Session, *discordgo.Resumed) { b.connected.Store(true) }

func (b *Bot) handleDisconnect(*discordgo.Session, *discordgo.Disconnect) { b.connected.Store(false) }

// applicationID is the OAuth2 client ID when DISCORD_CLIENT_ID is unset:
// the application from the Ready event, else the bot's user ID (the same
// for applications created since 2016).
func (b *Bot) applicationID() string {
	if id, _ := b.appID.Load().(string); id != "" {
		return id
	}
	return botUserID(b.Session)
}

func (b *Bot) botInfo() panels.BotInfo {
	s := b.Session
	info := panels.BotInfo{Online: b.connected.Load()}
	// LastHeartbeatAck is written under the session lock.
	s.RLock()
	info.Latency = s.HeartbeatLatency()
	s.RUnlock()

	st := s.State
	st.RLock()
	defer st.RUnlock()
	if st.User != nil {
		info.User = st.User.Username
	}
	for _, g := range st.Guilds {
		info.Guilds = append(info.Guilds, panels.GuildInfo{ID: g.ID, Name: g.Name, Members: g.MemberCount})
	}
	slices.SortFunc(info.Guilds, func(a, b panels.GuildInfo) int {
		if a.Name != b.Name {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return info
}

// stateNames resolves names from the gateway cache only.
type stateNames struct {
	s *discordgo.Session
}

func (n stateNames) Guild(id string) string {
	g, err := n.s.State.Guild(id)
	if err != nil {
		return ""
	}
	n.s.State.RLock()
	defer n.s.State.RUnlock()
	return g.Name
}

func (n stateNames) Channel(id string) string {
	ch, err := n.s.State.Channel(id)
	if err != nil {
		return ""
	}
	n.s.State.RLock()
	defer n.s.State.RUnlock()
	return ch.Name
}

func (n stateNames) Member(guildID, userID string) string {
	m, err := n.s.State.Member(guildID, userID)
	if err != nil {
		return ""
	}
	n.s.State.RLock()
	defer n.s.State.RUnlock()
	return memberDisplayName(m)
}

func memberDisplayName(m *discordgo.Member) string {
	switch {
	case m.Nick != "":
		return m.Nick
	case m.User == nil:
		return ""
	case m.User.GlobalName != "":
		return m.User.GlobalName
	}
	return m.User.Username
}

// memberAPI is the REST call the access check makes with the bot token.
type memberAPI interface {
	GuildMember(guildID, userID string, options ...discordgo.RequestOption) (*discordgo.Member, error)
}

// stateGuilds is the dashboard's GuildSource: guilds, owners and role
// permissions from the gateway state (kept current through the Guilds
// intent), and the member's roles over REST with the bot token. The member
// cache is not used for roles: without the privileged Server Members intent
// it misses role changes.
type stateGuilds struct {
	s   *discordgo.Session
	api memberAPI // nil means s
}

func (g stateGuilds) GuildIDs() []string {
	st := g.s.State
	st.RLock()
	defer st.RUnlock()
	ids := make([]string, 0, len(st.Guilds))
	for _, gd := range st.Guilds {
		ids = append(ids, gd.ID)
	}
	return ids
}

func (g stateGuilds) Member(ctx context.Context, guildID, userID string) (dashboard.GuildMember, bool, error) {
	guild, err := g.s.State.Guild(guildID)
	if err != nil {
		// Not in the state yet (just started or re-identifying): unknown,
		// not a refusal, so the answer is not cached.
		return dashboard.GuildMember{}, false, fmt.Errorf("%w: guild %s", dashboard.ErrGuildsNotLoaded, guildID)
	}
	gm := dashboard.GuildMember{GuildID: guildID, RolePerms: map[string]int64{}}
	g.s.State.RLock()
	if guild.Unavailable || len(guild.Roles) == 0 {
		// A Ready stub: roles and owner arrive with GUILD_CREATE.
		g.s.State.RUnlock()
		return dashboard.GuildMember{}, false, fmt.Errorf("%w: guild %s", dashboard.ErrGuildsNotLoaded, guildID)
	}
	gm.OwnerID = guild.OwnerID
	for _, r := range guild.Roles {
		gm.RolePerms[r.ID] = r.Permissions
	}
	g.s.State.RUnlock()

	var api memberAPI = g.s
	if g.api != nil {
		api = g.api
	}
	m, err := api.GuildMember(guildID, userID, discordgo.WithContext(ctx))
	if err != nil {
		var rest *discordgo.RESTError
		if errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusNotFound {
			return dashboard.GuildMember{}, false, nil
		}
		return dashboard.GuildMember{}, false, err
	}
	gm.MemberRoles = m.Roles
	return gm, true, nil
}
