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
			name: "seeded admin row (empty hash but admin role) is not anonymous",
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
