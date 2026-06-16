package auth_test

import (
	"context"
	"log/slog"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

type stubPlayerByID struct {
	player *Player
}

func (s *stubPlayerByID) GetPlayerByID(_ context.Context, _ int64) (*Player, error) {
	return s.player, nil
}

type spyMigrator struct {
	called bool
}

func (s *spyMigrator) ReattributeGames(_ context.Context, _, _ int64) (int64, error) {
	s.called = true

	return 0, nil
}

func TestMigrateGamesAfterSignIn_PriorRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prior      *Player
		wantCalled bool
	}{
		{
			name:       "anonymous prior row is migrated",
			prior:      &Player{ID: 1, Role: RolePlayer},
			wantCalled: true,
		},
		{
			// An OAuth-claimed row (email set, no password) is a real account,
			// so its games must not be reattributed onto the signed-in account.
			name:       "oauth-claimed prior row is not migrated",
			prior:      &Player{ID: 1, Email: "user@example.com", Role: RolePlayer},
			wantCalled: false,
		},
		{
			name:       "password prior row is not migrated",
			prior:      &Player{ID: 1, PasswordHash: "$2a$bcrypted", Role: RolePlayer},
			wantCalled: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			players := &stubPlayerByID{player: tc.prior}
			migrator := &spyMigrator{}
			prior := tc.prior.ID

			MigrateGamesAfterSignIn(
				t.Context(), slog.New(slog.DiscardHandler), players, migrator, &prior, int64(99),
			)

			if got, want := migrator.called, tc.wantCalled; got != want {
				t.Errorf("ReattributeGames called = %v, want %v", got, want)
			}
		})
	}
}
