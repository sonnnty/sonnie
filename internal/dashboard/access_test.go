package dashboard

import (
	"context"
	"testing"
)

func TestCanManage(t *testing.T) {
	base := GuildMember{GuildID: "g", OwnerID: "owner", RolePerms: map[string]int64{
		"g":     1 << 10,
		"mod":   PermManageGuild,
		"admin": PermAdministrator | 1<<11,
		"plain": 1 << 11,
	}}
	cases := []struct {
		user     string
		roles    []string
		everyone int64
		want     bool
	}{
		{"owner", nil, 0, true},
		{"u", []string{"mod"}, 0, true},
		{"u", []string{"plain", "admin"}, 0, true},
		{"u", []string{"plain"}, 0, false},
		{"u", []string{"unknown-role"}, 0, false},
		{"u", nil, PermManageGuild, true}, // granted to @everyone
	}
	for _, c := range cases {
		m := base
		m.MemberRoles = c.roles
		m.RolePerms = map[string]int64{}
		for k, v := range base.RolePerms {
			m.RolePerms[k] = v
		}
		m.RolePerms["g"] |= c.everyone
		if got := CanManage(m, c.user); got != c.want {
			t.Errorf("user %s roles %v everyone %b: got %v", c.user, c.roles, c.everyone, got)
		}
	}
}

func TestGuildAccess_GuildAndRoles(t *testing.T) {
	ctx := context.Background()
	g := newFakeGuilds() // guildA: 1 mod, 2 admin, 3 member, 4 none; guildB: 3, 4 (owner)
	cases := []struct {
		name   string
		access GuildAccess
		user   string
		want   bool
	}{
		{"any guild: owner of B", GuildAccess{Guilds: g}, "4", true},
		{"guild A only: owner of B is out", GuildAccess{Guilds: g, GuildID: guildA}, "4", false},
		{"guild A only: mod", GuildAccess{Guilds: g, GuildID: guildA}, "1", true},
		{"role: member has it", GuildAccess{Guilds: g, GuildID: guildA, RoleIDs: []string{roleMember}}, "3", true},
		{"role: admin without it is out", GuildAccess{Guilds: g, GuildID: guildA, RoleIDs: []string{roleMember}}, "2", false},
		{"role: one of several", GuildAccess{Guilds: g, GuildID: guildA, RoleIDs: []string{"999", roleMod}}, "1", true},
		{"role: not in that guild", GuildAccess{Guilds: g, GuildID: guildB, RoleIDs: []string{roleMember}}, "3", false},
		{"allowed user always", GuildAccess{Guilds: g, GuildID: guildA, RoleIDs: []string{"999"}, Allowed: []string{"4"}}, "4", true},
	}
	for _, c := range cases {
		got, err := c.access.Authorize(ctx, c.user)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}
