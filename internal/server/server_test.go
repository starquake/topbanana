package server_test

import (
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/mailer"
	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	srv := New(
		slog.New(slog.DiscardHandler),
		&store.Stores{}, &game.Service{},
		Realtime{
			LeaderboardHub: leaderboard.NewHub(),
			SessionService: &livesession.Service{},
			SessionHub:     livesession.NewHub(),
		},
		&config.Config{},
		Mail{Tester: mailer.NewTester(mailer.NewNoop())},
	)

	if srv == nil {
		t.Error("srv is nil")
	}
}
