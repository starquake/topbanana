// Package server contains everything related to the Server
package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/store"
)

// Realtime bundles the process-local pub/sub and live-session deps so they
// travel as one argument through server.New / addRoutes rather than blowing
// the per-function argument limit as the realtime surface grows. LeaderboardHub
// is the SSE leaderboard stream's hub (the same instance wired into the game
// service via SetLeaderboardPublisher). SessionService + SessionHub are the
// hosted live-session service and its SSE tick hub; the same instances the
// runner goroutine publishes through (MP-5 / #682).
type Realtime struct {
	LeaderboardHub *leaderboard.Hub
	SessionService *livesession.Service
	SessionHub     *livesession.Hub
}

// Mail bundles the mailer deps so they travel as one argument through
// server.New / addRoutes. Tester is the ring-buffer wrapper around the live
// mailer (no-op when SMTP is unconfigured); Status is the safe view the
// diagnostics page renders so the admin can confirm wiring without exposing
// credentials. Tasks tracks the detached email-dispatch goroutines the auth /
// profile / admin handlers spawn so a graceful shutdown drains them before the
// DB closes (#740); it may be nil, in which case those dispatches run
// untracked.
type Mail struct {
	Tester *mailer.Tester
	Status mailer.StatusView
	Tasks  *bgtasks.Tracker
}

// New creates a new server. realtime carries the process-local pub/sub hubs
// and the live-session service. mail bundles the mailer wiring plus the
// background-task tracker shutdown drains.
func New(
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	realtime Realtime,
	cfg *config.Config,
	mail Mail,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, stores, gameService, realtime, cfg, mail)
	var handler http.Handler = mux
	handler = logRequests(logger, handler)
	// recoverPanic is the OUTERMOST wrapper so a handler panic still
	// captures the request fields logRequests would have recorded and
	// the 500 reaches the client cleanly instead of leaking a half-
	// written response (#346).
	handler = recoverPanic(logger, handler)

	return handler
}
