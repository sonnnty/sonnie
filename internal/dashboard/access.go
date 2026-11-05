package dashboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrGuildsNotLoaded means the bot does not know its guilds yet (just started
// or reconnecting). It makes the access check indeterminate: the request is
// answered 503 and nothing is cached, so the page retries.
var ErrGuildsNotLoaded = errors.New("guild state not loaded yet")

// Authorizer decides whether a Discord user may see the dashboard.
type Authorizer interface {
	Authorize(ctx context.Context, userID string) (bool, error)
}

// Discord permission bits that grant access.
const (
	PermAdministrator int64 = 1 << 3
	PermManageGuild   int64 = 1 << 5
)

// GuildMember is what the access check needs to know about a user in one
// guild, read with the bot's own credentials (gateway state or REST).
type GuildMember struct {
	OwnerID string
	// RolePerms maps each role ID of the guild to its permissions. The
	// @everyone role has the guild's ID.
	RolePerms   map[string]int64
	GuildID     string
	MemberRoles []string
}

// GuildSource lists the bot's guilds and looks up members in them.
type GuildSource interface {
	GuildIDs() []string
	// Member returns the user's membership; found is false when they are
	// not in the guild.
	Member(ctx context.Context, guildID, userID string) (m GuildMember, found bool, err error)
}

// CanManage reports whether userID owns the guild or has Administrator or
// Manage Server there through @everyone or one of their roles.
func CanManage(m GuildMember, userID string) bool {
	if m.OwnerID != "" && m.OwnerID == userID {
		return true
	}
	perms := m.RolePerms[m.GuildID]
	for _, r := range m.MemberRoles {
		perms |= m.RolePerms[r]
	}
	return perms&(PermAdministrator|PermManageGuild) != 0
}

// GuildAccess lets in the users listed in Allowed, and members of the bot's
// guilds (only GuildID when it is set) who either have one of RoleIDs or, when
// RoleIDs is empty, can manage the guild (Administrator or Manage Server).
type GuildAccess struct {
	Allowed []string
	Guilds  GuildSource
	// GuildID, if set, is the only guild whose members may log in.
	GuildID string
	// RoleIDs, if set, replaces the manage check: any one of them lets in.
	RoleIDs []string
}

// grants reports whether membership m lets userID in.
func (a GuildAccess) grants(m GuildMember, userID string) bool {
	if len(a.RoleIDs) == 0 {
		return CanManage(m, userID)
	}
	for _, r := range m.MemberRoles {
		if slices.Contains(a.RoleIDs, r) {
			return true
		}
	}
	return false
}

// Authorize implements Authorizer. A guild whose lookup fails does not deny
// access when another guild grants it; it only matters when nothing grants
// access, and then the error is returned so the caller can tell a refusal
// from an outage.
func (a GuildAccess) Authorize(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	if slices.Contains(a.Allowed, userID) {
		return true, nil
	}
	if a.Guilds == nil {
		return false, nil
	}
	var errs []string
	notLoaded := false
	guilds := a.Guilds.GuildIDs()
	if a.GuildID != "" {
		guilds = []string{a.GuildID}
	}
	if len(guilds) == 0 {
		return false, ErrGuildsNotLoaded
	}
	for _, gid := range guilds {
		m, found, err := a.Guilds.Member(ctx, gid, userID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("guild %s: %v", gid, err))
			notLoaded = notLoaded || errors.Is(err, ErrGuildsNotLoaded)
			continue
		}
		if found && a.grants(m, userID) {
			return true, nil
		}
	}
	if len(errs) > 0 {
		// One line, so it logs as one entry.
		if notLoaded {
			return false, fmt.Errorf("%w (%s)", ErrGuildsNotLoaded, strings.Join(errs, "; "))
		}
		return false, errors.New(strings.Join(errs, "; "))
	}
	return false, nil
}
