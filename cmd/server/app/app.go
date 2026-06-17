// Package app contains the main entrypoint for the server.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/clientapi"
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
	defaultWriteTimeout = 10 * time.Second
	// tokenSweepInterval is the wall-clock gap between background
	// sweeps of the verify and reset token tables. The startup sweep
	// already runs once on boot; this keeps a long-running deploy from
	// accumulating orphans between restarts (#472). One hour is
	// frequent enough that an SMTP-orphan row never lingers more than
	// a tick past its TTL, infrequent enough that the DELETE shows up
	// once an hour in the slow-query log.
	tokenSweepInterval = time.Hour
)

// Option configures a [Run] invocation. Used by integration tests to
// override values that have no env-var hook (the HTTP server's write
// timeout and the SSE handlers' heartbeat intervals, used by the SSE
// heartbeat regression tests to keep the assertion inside a sub-second
// window without leaning on the production 10s / 25s defaults). No
// production caller passes options.
type Option func(*options)

type options struct {
	writeTimeout                  time.Duration
	leaderboardHeartbeatInterval  time.Duration
	sessionEventHeartbeatInterval time.Duration
}

// WithWriteTimeout overrides the HTTP server's WriteTimeout. The SSE
// heartbeat regression tests shrink it so the assertion that the stream
// stays alive past WriteTimeout AND past one heartbeat tick runs in a
// sub-second window. Zero or negative values are ignored.
func WithWriteTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.writeTimeout = d
		}
	}
}

// WithLeaderboardHeartbeatInterval overrides the heartbeat interval for
// the leaderboard SSE stream. The heartbeat regression test shrinks it so
// the "at least one heartbeat fired" assertion runs in milliseconds. Zero
// or negative values are ignored.
func WithLeaderboardHeartbeatInterval(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.leaderboardHeartbeatInterval = d
		}
	}
}

// WithSessionEventHeartbeatInterval overrides the heartbeat interval for
// the session-events SSE stream. The heartbeat regression test shrinks it
// so the "at least one heartbeat fired" assertion runs in milliseconds.
// Zero or negative values are ignored.
func WithSessionEventHeartbeatInterval(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.sessionEventHeartbeatInterval = d
		}
	}
}

// newRealtime bundles the process-local pub/sub deps and the resolved
// streaming-timer settings into a [server.Realtime] for [server.New].
func newRealtime(
	leaderboardHub *leaderboard.Hub,
	sessionService *livesession.Service,
	sessionHub *livesession.Hub,
	o options,
) server.Realtime {
	return server.Realtime{
		LeaderboardHub:                leaderboardHub,
		SessionService:                sessionService,
		SessionHub:                    sessionHub,
		LeaderboardHeartbeatInterval:  o.leaderboardHeartbeatInterval,
		SessionEventHeartbeatInterval: o.sessionEventHeartbeatInterval,
	}
}

// resolveOptions builds the [Run] options struct from defaults plus any
// caller-supplied overrides.
func resolveOptions(opts []Option) options {
	o := options{
		writeTimeout:                  defaultWriteTimeout,
		leaderboardHeartbeatInterval:  clientapi.DefaultLeaderboardHeartbeatInterval,
		sessionEventHeartbeatInterval: clientapi.DefaultSessionEventHeartbeatInterval,
	}
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// Run starts the application server, connects to the database, runs migrations, and listens for incoming requests.
func Run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
	ln net.Listener,
	opts ...Option,
) error {
	o := resolveOptions(opts)

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

	if err = ensureMediaDir(signalCtx, cfg.MediaDir, logger); err != nil {
		return err
	}

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

	realtime := newRealtime(leaderboardHub, sessionService, sessionHub, o)
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

	return runHTTPServer(ctx, signalCtx, ln, srv, emailTasks, logger, o.writeTimeout)
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
// beat (3s default; the e2e's 500ms shrinks both). The idle-close timeout tracks
// SESSION_IDLE_CLOSE independently of the beats (0 falls back to the runner's
// 30-minute default). The tick interval tracks the runner beat so a shrunk beat
// is observed promptly without spinning the loop when the beat is the default.
func runnerConfig(cfg *config.Config) livesession.RunnerConfig {
	rc := livesession.RunnerConfig{
		QuestionReadBeat: cfg.RevealDelay,
		IdleCloseTimeout: cfg.SessionIdleClose,
	}
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

	// Likewise the round-intro beat: a shrunk runner beat leaves the round-intro
	// card too brief for a loaded browser to observe before the phase advances
	// (#859).
	if cfg.SessionRoundIntroBeat > 0 {
		rc.RoundIntroBeat = cfg.SessionRoundIntroBeat
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
