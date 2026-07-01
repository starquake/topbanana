package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func newDemoTestServer(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()

	return New(
		slog.New(slog.DiscardHandler),
		&store.Stores{}, &game.Service{},
		Realtime{
			LeaderboardHub: leaderboard.NewHub(),
			SessionService: &livesession.Service{},
			SessionHub:     livesession.NewHub(),
		},
		cfg,
		Mail{Tester: mailer.NewTester(mailer.NewNoop())},
	)
}

func TestServer_DemoModeRoutes(t *testing.T) {
	t.Parallel()

	// The server reads demo mode from the passed config (post-Parse state), so
	// the subtests build the config directly: demo on derives ProfileEnabled
	// off; demo off leaves it on.
	t.Run("enabled: /profile route not registered", func(t *testing.T) {
		t.Parallel()
		h := newDemoTestServer(t, &config.Config{DemoMode: true})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/profile", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("GET /profile code = %d, want %d", got, want)
		}
	})

	t.Run("disabled: /demo is a normal 404", func(t *testing.T) {
		t.Parallel()
		h := newDemoTestServer(t, &config.Config{ProfileEnabled: true})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/demo", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("GET /demo code = %d, want %d", got, want)
		}
	})
}
