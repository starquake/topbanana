// Package app contains the main entrypoint for the server.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/version"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	// idleTimeout caps how long a keep-alive connection can sit unused before the
	// server closes it. Without this, idle connections behind a pooled proxy or
	// CDN linger indefinitely and leak file descriptors. 120s is the conventional
	// upper bound; long enough for legitimate keep-alive reuse, short enough to
	// reclaim sockets from stale clients.
	idleTimeout     = 120 * time.Second
	shutdownTimeout = 5 * time.Second
	// tokenSweepInterval is the wall-clock gap between background
	// sweeps of the verify and reset token tables. The startup sweep
	// already runs once on boot; this keeps a long-running deploy from
	// accumulating orphans between restarts (#472). One hour is
	// frequent enough that an SMTP-orphan row never lingers more than
	// a tick past its TTL, infrequent enough that the DELETE shows up
	// once an hour in the slow-query log.
	tokenSweepInterval = time.Hour
)

// Run starts the application server, connects to the database, runs migrations, and listens for incoming requests.
func Run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
	ln net.Listener,
) error {
	var err error
	// SIGTERM is what Docker / k8s send on container stop; without it
	// the graceful-shutdown path (httpServer.Shutdown + DB close) never
	// runs in prod and the container is hard-killed at the
	// orchestrator's grace timeout (#342).
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg, err := config.Parse(getenv)
	if err != nil {
		msg := "error parsing config"
		logger.ErrorContext(signalCtx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}
	logConfigSummary(signalCtx, logger, cfg)

	conn, err := setupDB(signalCtx, cfg.DatabaseConfig(), logger)
	if err != nil {
		return err
	}
	defer func() {
		conErr := conn.Close()
		if conErr != nil {
			logger.ErrorContext(signalCtx, "error closing database connection", slog.Any("err", conErr))
		}
	}()

	envtag.Set(cfg.EnvTitleTag())
	version.SetEnv(cfg.AppEnvironment)

	stores := store.New(conn, logger)
	sweepExpiredAtStartup(signalCtx, logger, stores)
	// The startup sweep only fires once. Tokens minted in the hours
	// between deploys (and their flaky-SMTP orphan rows) need a
	// periodic broom too; the hourly tick keeps both tables bounded on
	// long-running deploys without burning a goroutine per row (#472).
	go runTokenSweep(
		signalCtx, logger,
		stores.VerifyTokens, stores.ResetTokens, stores.Invites, stores.Retention,
		tokenSweepInterval,
	)
	gameService, leaderboardHub := newGameService(cfg, logger, stores)
	// Own the runner's context so shutdown waits for its goroutine to exit
	// before Run returns - else it logs past test teardown under -race (#608).
	runnerCtx, stopRunner := context.WithCancel(signalCtx)
	sessionService, sessionHub, runnerDone := startSessionRunner(runnerCtx, cfg, logger, stores, gameService)
	defer func() {
		stopRunner()
		<-runnerDone
	}()

	realtime := server.Realtime{
		LeaderboardHub: leaderboardHub,
		SessionService: sessionService,
		SessionHub:     sessionHub,
	}
	srv, emailTasks, err := buildServer(signalCtx, cfg, logger, stores, gameService, realtime)
	if err != nil {
		return err
	}
	if ln == nil {
		ln, err = listener(signalCtx, cfg, logger)
		if err != nil {
			return fmt.Errorf("error creating listener: %w", err)
		}
	} else {
		logger.InfoContext(signalCtx, "listener overridden")
	}

	return runHTTPServer(ctx, signalCtx, ln, srv, emailTasks, logger)
}

// buildServer constructs the mailer, the background-task tracker, and the HTTP
// handler. It returns the tracker alongside the handler so runHTTPServer can
// drain the detached email-dispatch goroutines the handlers spawn after the
// HTTP server stops accepting requests and before the deferred conn.Close
// runs, so a dispatch never writes to a closed DB on shutdown (#740, #741).
func buildServer(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	realtime server.Realtime,
) (http.Handler, *bgtasks.Tracker, error) {
	mailerTester, mailerStatus, err := buildMailer(ctx, cfg, logger)
	if err != nil {
		return nil, nil, err
	}
	emailTasks := bgtasks.New()
	mail := server.Mail{Tester: mailerTester, Status: mailerStatus, Tasks: emailTasks}

	return server.New(logger, stores, gameService, realtime, cfg, mail), emailTasks, nil
}

// newGameService builds the game service with the reveal-delay override
// applied and the leaderboard hub wired as its publisher. The hub is the
// process-local pub/sub for the leaderboard SSE stream (#239): the same
// instance feeds the game service (publisher) and the server (subscriber
// side) so submitted answers fan out to live viewers. Returns both so the
// server can subscribe to the same hub.
func newGameService(cfg *config.Config, logger *slog.Logger, stores *store.Stores) (*game.Service, *leaderboard.Hub) {
	gameService := game.NewService(stores.Games, stores.Quizzes, logger)
	if cfg.RevealDelay > 0 {
		gameService.SetRevealDelay(cfg.RevealDelay)
	}
	leaderboardHub := leaderboard.NewHub()
	gameService.SetLeaderboardPublisher(leaderboardHub)

	return gameService, leaderboardHub
}

// startSessionRunner wires the hosted live-session service, its SSE tick hub,
// and the runner over one set of instances: the service and runner both
// publish ticks through the hub, the runner advances phases on the server
// clock, and Start hands a started session to the runner via SetAdvancer. The
// runner is one goroutine bound to ctx (the shutdown context), so it stops
// before the DB closes. Returns the service + hub for server wiring plus a
// done channel that closes when the runner goroutine exits, so the caller
// can wait for it on shutdown rather than leaking a still-logging goroutine
// past Run (MP-5 / #682, #608).
func startSessionRunner(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	stores *store.Stores,
	scorer livesession.Scorer,
) (*livesession.Service, *livesession.Hub, <-chan struct{}) {
	service := livesession.NewService(stores.LiveSessions, stores.Quizzes, logger)
	hub := livesession.NewHub()
	service.SetPublisher(hub)
	service.SetStartCountdown(cfg.SessionStartCountdown)
	runner := livesession.NewRunner(stores.LiveSessions, stores.Quizzes, hub, scorer, logger, runnerConfig(cfg))
	service.SetAdvancer(runner)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()

	return service, hub, done
}

// runnerBeatTickDivisor keeps the runner's per-beat scan tick a fraction of
// the configured beat so a shrunk beat is observed promptly without spinning.
const runnerBeatTickDivisor = 4

// runnerConfig builds the live-session runner config from cfg. When
// SESSION_RUNNER_BEAT is set (the e2e / integration suites shrink it), it
// drives the round-intro, reveal, and between-rounds beats so a hosted game
// advances quickly; otherwise those fall back to the runner's built-in
// defaults. The question read beat tracks REVEAL_DELAY independently of
// SESSION_RUNNER_BEAT, so the live read beat matches the solo game's pre-answer
// beat (3s default; the e2e's 500ms shrinks both). The tick interval tracks the
// runner beat so a shrunk beat is observed promptly without spinning the loop
// when the beat is the default.
func runnerConfig(cfg *config.Config) livesession.RunnerConfig {
	rc := livesession.RunnerConfig{QuestionReadBeat: cfg.RevealDelay}
	if cfg.SessionRunnerBeat <= 0 {
		return rc
	}
	beat := cfg.SessionRunnerBeat
	rc.BeatInterval = max(beat/runnerBeatTickDivisor, time.Millisecond)
	rc.RoundIntroBeat = beat
	rc.RevealBeat = beat
	rc.RoundResultsBeat = beat

	// The reveal beat can be lengthened independently so a shrunk runner beat
	// still leaves the revealed answer observable (e.g. in the e2e suite).
	if cfg.SessionRevealBeat > 0 {
		rc.RevealBeat = cfg.SessionRevealBeat
	}

	return rc
}

// tokenSweeper is the slice of the verify / reset stores the periodic
// sweep calls. Narrow interface so the unit test can drive the loop
// without standing up the real DB.
type tokenSweeper interface {
	DeleteExpiredVerifyTokens(ctx context.Context) error
}

type resetTokenSweeper interface {
	DeleteExpiredResetTokens(ctx context.Context) error
}

type inviteSweeper interface {
	DeleteExpiredInvites(ctx context.Context) error
}

// retentionSweeper is the slice of the retention store the sweep calls.
// Narrow interface so the unit test can drive the loop without a real DB.
// Each method takes its retention window in days; the cutoff date is then
// computed in SQL.
type retentionSweeper interface {
	SweepStaleAnonymousPlayers(ctx context.Context, days int) error
	SweepAbandonedGames(ctx context.Context, days int) error
}

// runRetentionSweep runs both data-retention sweeps once with the configured
// retention windows, logging each failure at warn so a transient error in one
// does not skip the other or abort the surrounding token sweep.
func runRetentionSweep(ctx context.Context, logger *slog.Logger, retention retentionSweeper) {
	if err := retention.SweepStaleAnonymousPlayers(ctx, store.AnonymousRetentionDays); err != nil {
		logger.WarnContext(ctx, "anonymous-player retention sweep failed", slog.Any("err", err))
	}
	if err := retention.SweepAbandonedGames(ctx, store.AbandonedGameDays); err != nil {
		logger.WarnContext(ctx, "abandoned-game retention sweep failed", slog.Any("err", err))
	}
}

// sweepExpiredAtStartup runs the one-shot expiry sweep across the verify,
// reset, and invite token tables plus the data-retention sweeps (stale
// anonymous players and abandoned games) at boot, before the periodic
// sweep goroutine takes over. Each failure is logged at warn and the others
// still run; a single table's transient error must not skip the rest.
func sweepExpiredAtStartup(ctx context.Context, logger *slog.Logger, stores *store.Stores) {
	if err := stores.VerifyTokens.DeleteExpiredVerifyTokens(ctx); err != nil {
		logger.WarnContext(ctx, "verify-token sweep at startup failed", slog.Any("err", err))
	}
	if err := stores.ResetTokens.DeleteExpiredResetTokens(ctx); err != nil {
		logger.WarnContext(ctx, "reset-token sweep at startup failed", slog.Any("err", err))
	}
	if err := stores.Invites.DeleteExpiredInvites(ctx); err != nil {
		logger.WarnContext(ctx, "invite sweep at startup failed", slog.Any("err", err))
	}
	runRetentionSweep(ctx, logger, stores.Retention)
}

// runTokenSweep ticks at interval and on each iteration runs the verify,
// reset, and invite token expiry sweeps plus the data-retention sweeps
// (stale anonymous players and abandoned games). Returns when ctx is
// cancelled (which is the signal-driven shutdown context, so a graceful
// shutdown stops the sweep before the DB is closed). A sweep failure is
// logged at warn and the loop continues; one bad tick should not silence
// the next hour's pass.
func runTokenSweep(
	ctx context.Context,
	logger *slog.Logger,
	verify tokenSweeper,
	reset resetTokenSweeper,
	invites inviteSweeper,
	retention retentionSweeper,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := verify.DeleteExpiredVerifyTokens(ctx); err != nil {
				logger.WarnContext(ctx, "verify-token periodic sweep failed", slog.Any("err", err))
			}
			if err := reset.DeleteExpiredResetTokens(ctx); err != nil {
				logger.WarnContext(ctx, "reset-token periodic sweep failed", slog.Any("err", err))
			}
			if err := invites.DeleteExpiredInvites(ctx); err != nil {
				logger.WarnContext(ctx, "invite periodic sweep failed", slog.Any("err", err))
			}
			runRetentionSweep(ctx, logger, retention)
		}
	}
}

func runHTTPServer(
	ctx, signalCtx context.Context,
	ln net.Listener,
	srv http.Handler,
	emailTasks *bgtasks.Tracker,
	logger *slog.Logger,
) error {
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		Handler:           srv,
	}

	g, gCtx := errgroup.WithContext(signalCtx)

	g.Go(func() error {
		logger.InfoContext(gCtx, "listening on "+ln.Addr().String(), slog.String("addr", ln.Addr().String()))
		addr := ln.Addr().String()
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/admin to manage quizzes", addr))
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/ to play", addr))
		httpErr := httpServer.Serve(ln)
		if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
			msg := "error listening and serving"
			logger.ErrorContext(signalCtx, msg, slog.Any("err", httpErr))

			return fmt.Errorf("%s: %w", msg, httpErr)
		}

		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		// make a new context for the Shutdown
		// use the root ctx to ensure shutdown has its own timeout even though signalCtx is already canceled
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
		defer shutdownCancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		// Drain the detached email-dispatch goroutines AFTER Shutdown stops
		// the listener and BEFORE Run's deferred conn.Close runs, so a
		// dispatch can't write to a closed DB (#740, #741). The bound is
		// detached from ctx via WithoutCancel: at shutdown ctx is already
		// cancelled (signal-driven, and the integration harness cancels the
		// same ctx it passes to Run), so a plain WithTimeout(ctx, ...) would
		// fire instantly and skip the wait. The dispatches carry per-send
		// timeouts longer than shutdownTimeout, so a stuck SMTP must not pin
		// shutdown - draining what it can within the bound and giving up is
		// the right trade.
		drainCtx, drainCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer drainCancel()
		if drainErr := emailTasks.Wait(drainCtx); drainErr != nil {
			logger.WarnContext(ctx, "gave up waiting for background email dispatches", slog.Any("err", drainErr))
		}
		if shutdownErr != nil {
			logger.ErrorContext(shutdownCtx, "error shutting down server", slog.Any("err", shutdownErr))

			return fmt.Errorf("error shutting down server: %w", shutdownErr)
		}

		return nil
	})

	err := g.Wait()
	if err != nil {
		return fmt.Errorf("error running server: %w", err)
	}

	return nil
}

// buildMailer constructs the mailer + status view the admin diagnostics
// page consumes (#321). When SMTP is unconfigured we wrap the no-op
// mailer in a Tester so the same ring buffer surfaces "tried to send
// but SMTP is off" entries on the diagnostics page; when SMTP is
// configured the Tester wraps the real go-mail-backed mailer.
//
// Returns an error only when the SMTPConfigured() guard passes but the
// inner SMTP constructor refuses the cfg - that path is unreachable
// today because config.Parse enforces the same triple, but the wrap
// keeps a future SMTP-side validation tweak from silently booting a
// broken mailer.
func buildMailer(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
) (*mailer.Tester, mailer.StatusView, error) {
	smtpCfg := mailer.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		TLS:      cfg.SMTPTLS,
	}
	if !cfg.SMTPConfigured() {
		logger.InfoContext(
			ctx,
			"mailer disabled (no SMTP_HOST/SMTP_PORT/SMTP_FROM); send attempts return ErrNotConfigured",
		)

		return mailer.NewTester(mailer.NewNoop()), mailer.NewStatusView(smtpCfg, false, cfg.BaseURL), nil
	}

	inner, err := mailer.NewSMTP(smtpCfg)
	if err != nil {
		return nil, mailer.StatusView{}, fmt.Errorf("build mailer: %w", err)
	}
	logger.InfoContext(ctx, "mailer configured",
		slog.String("host", cfg.SMTPHost),
		slog.Int("port", cfg.SMTPPort),
		slog.Bool("tls", cfg.SMTPTLS),
	)
	if cfg.BaseURL == "" {
		logger.WarnContext(ctx, "email links disabled: BASE_URL is unset while SMTP is configured")
	}

	return mailer.NewTester(inner), mailer.NewStatusView(smtpCfg, true, cfg.BaseURL), nil
}

func setupDB(signalCtx context.Context, dbc config.DatabaseConfig, logger *slog.Logger) (*sql.DB, error) {
	conn, err := database.Open(
		signalCtx,
		dbc.Driver,
		dbc.URI,
		dbc.MaxOpenConns,
		dbc.MaxIdleConns,
		dbc.ConnMaxLifetime,
	)
	if err != nil {
		logger.ErrorContext(signalCtx, "error opening database connection", slog.Any("err", err))

		return nil, fmt.Errorf("error opening database connection: %w", err)
	}

	if err = database.Migrate(conn); err != nil {
		msg := "error migrating database"
		logger.ErrorContext(signalCtx, msg, slog.Any("err", err))

		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	return conn, nil
}

// logConfigSummary emits the operator-relevant config knobs at startup so
// debugging "the cookie was/wasn't Secure" or "I thought I'd disabled
// Google sign-in" doesn't require a fresh read of the env file. APP_ENV
// is logged raw so an unset value shows as the empty string - that is
// itself a meaningful signal (fail-secure defaults kick in).
func logConfigSummary(ctx context.Context, logger *slog.Logger, cfg *config.Config) {
	logger.InfoContext(ctx, "config parsed",
		slog.String("app_env", cfg.AppEnvironment),
		slog.Bool("secure_cookies", cfg.SecureCookies()),
		slog.Bool("registration_enabled", cfg.RegistrationEnabled),
		slog.Bool("google_login_enabled", cfg.GoogleLoginEnabled()),
	)
}

func listener(ctx context.Context, cfg *config.Config, logger *slog.Logger) (net.Listener, error) {
	logger.InfoContext(ctx, "creating listener based on config")
	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		logger.ErrorContext(ctx, "error listening on "+cfg.Host+":"+cfg.Port, slog.Any("err", err))

		return nil, fmt.Errorf("error listening on %s:%s: %w", cfg.Host, cfg.Port, err)
	}

	return ln, nil
}
