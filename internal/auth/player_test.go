package auth_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

func TestPlayer_IsAnonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p    Player
		want bool
	}{
		{
			name: "anonymous player (empty hash, player role)",
			p:    Player{PasswordHash: "", Role: RolePlayer},
			want: true,
		},
		{
			name: "credentialled player",
			p:    Player{PasswordHash: "$2a$bcrypted", Role: RolePlayer},
			want: false,
		},
		{
			// An OAuth-claimed row (email set, no password) is a real account,
			// so it must not read as anonymous or the migrator would move its games.
			name: "oauth-claimed player (email set, empty hash, player role) is not anonymous",
			p:    Player{PasswordHash: "", Email: "user@example.com", Role: RolePlayer},
			want: false,
		},
		{
			// After the #538 remap the seed admin (id=1) holds the Host
			// tier with a NULL password_hash; it must NOT read as a
			// claimable anonymous row.
			name: "seeded host row (empty hash but host role) is not anonymous",
			p:    Player{PasswordHash: "", Role: RoleHost},
			want: false,
		},
		{
			name: "admin row (empty hash but admin role) is not anonymous",
			p:    Player{PasswordHash: "", Role: RoleAdmin},
			want: false,
		},
		{
			name: "credentialled admin",
			p:    Player{PasswordHash: "$2a$bcrypted", Role: RoleAdmin},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := tc.p.IsAnonymous(), tc.want; got != want {
				t.Errorf("IsAnonymous() = %v, want %v", got, want)
			}
		})
	}
}

func TestPlayer_IsAdmin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want bool
	}{
		{"player", RolePlayer, false},
		{"host", RoleHost, false},
		{"admin", RoleAdmin, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := Player{Role: tc.role}
			if got, want := p.IsAdmin(), tc.want; got != want {
				t.Errorf("IsAdmin() = %v, want %v", got, want)
			}
		})
	}
}

func TestPlayer_CanHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want bool
	}{
		{"player", RolePlayer, false},
		{"host", RoleHost, true},
		{"admin", RoleAdmin, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := Player{Role: tc.role}
			if got, want := p.CanHost(), tc.want; got != want {
				t.Errorf("CanHost() = %v, want %v", got, want)
			}
		})
	}
}
