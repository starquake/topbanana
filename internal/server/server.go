// Package server contains everything related to the Server
package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/store"
)

// New creates a new server. leaderboardHub is the process-local pub/sub
// for SSE leaderboard streams; pass the same instance that's wired into
// gameService via SetLeaderboardPublisher.
func New(
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	leaderboardHub *leaderboard.Hub,
	cfg *config.Config,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, stores, gameService, leaderboardHub, cfg)
	var handler http.Handler = mux
	handler = logRequests(logger, handler)
	// recoverPanic is the OUTERMOST wrapper so a handler panic still
	// captures the request fields logRequests would have recorded and
	// the 500 reaches the client cleanly instead of leaking a half-
	// written response (#346).
	handler = recoverPanic(logger, handler)

	return handler
}
