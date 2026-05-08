package auth_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

func TestPlayer_IsAnonymous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p    auth.Player
		want bool
	}{
		{
			name: "anonymous player (empty hash, player role)",
			p:    auth.Player{PasswordHash: "", Role: auth.RolePlayer},
			want: true,
		},
		{
			name: "credentialled player",
			p:    auth.Player{PasswordHash: "$2a$bcrypted", Role: auth.RolePlayer},
			want: false,
		},
		{
			name: "seeded admin row (empty hash but admin role) is not anonymous",
			p:    auth.Player{PasswordHash: "", Role: auth.RoleAdmin},
			want: false,
		},
		{
			name: "credentialled admin",
			p:    auth.Player{PasswordHash: "$2a$bcrypted", Role: auth.RoleAdmin},
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
