package dashboard

import "testing"

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
