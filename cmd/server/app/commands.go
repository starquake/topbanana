package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

// Sentinel errors for ResetPassword; defined at package level so err113 stays
// quiet while keeping the failure modes typed for callers and tests. Tests
// in the external app_test package match on these via [errors.Is]; see
// export_test.go for the re-exports.
var (
	errResetEmailRequired      = errors.New("email is required")
	errResetPasswordTooShort   = errors.New("password too short")
	errResetPasswordTooLong    = errors.New("password too long")
	errResetUserNotFound       = errors.New("email not found")
	errResetEmptyInput         = errors.New("empty password input")
	errResetPasswordsDontMatch = errors.New("passwords do not match")
)

// resetWrap is the error-wrap prefix used by every ResetPassword failure
// path so error messages stay consistent and revive's add-constant linter
// stays quiet.
const resetWrap = "reset password: %w"

// ResetPassword reads a new password from stdin and overwrites the
// password_hash for the row identified by email. Operator-only tool
// for the lost-admin-password case. stdin echo is disabled when stdin
// is a terminal; otherwise the password is read up to the first
// newline so scripts can pipe. Lookup happens before the prompt so a
// typo'd email does not waste two password entries. Matching by email
// lines the operator's reset target up with the post-#446 login
// credential the player types into /login.
func ResetPassword(
	ctx context.Context,
	getenv func(string) string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	email string,
) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf(resetWrap, errResetEmailRequired)
	}

	dbc, err := config.ParseDatabase(getenv)
	if err != nil {
		return fmt.Errorf("reset password: parse config: %w", err)
	}

	conn, err := setupDB(ctx, dbc, logger)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", cerr))
		}
	}()

	players := store.NewPlayerStore(conn, logger)
	if _, lookupErr := players.GetPlayerByEmail(ctx, email); lookupErr != nil {
		if errors.Is(lookupErr, auth.ErrPlayerNotFound) {
			return fmt.Errorf("reset password: %w (%q)", errResetUserNotFound, email)
		}

		return fmt.Errorf(resetWrap, lookupErr)
	}

	password, err := readNewPassword(stdin, stdout)
	if err != nil {
		return err
	}

	hashed, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("reset password: hash password: %w", err)
	}

	if err := players.SetPlayerPasswordHash(ctx, email, hashed); err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			return fmt.Errorf("reset password: %w (%q)", errResetUserNotFound, email)
		}

		return fmt.Errorf(resetWrap, err)
	}

	logger.InfoContext(ctx, "password reset", slog.String("email", email))

	return nil
}

// errPromoteEmailRequired is wrapped by [PromoteAdmin] when the
// supplied email trims to empty; defined at package scope so callers
// and tests can match it via [errors.Is].
var errPromoteEmailRequired = errors.New("email is required")

// errPromoteEmailNotFound is wrapped by [PromoteAdmin] when no player row
// matches the supplied email.
var errPromoteEmailNotFound = errors.New("email not found")

// errSeedDemoDisabled is returned by SeedDemo when DEMO_MODE_ENABLED is not
// set; defined at package scope so callers can match it via [errors.Is].
var errSeedDemoDisabled = errors.New("DEMO_MODE_ENABLED is not set")

// PromoteAdmin looks up a player by email and sets them to the top tier
// (role = 'admin') (#538). This is a break-glass recovery tool: the first
// Admin normally comes from the first credentialled registration, so this
// exists only for when every Admin is locked out (lost passwords, deleted
// accounts) and someone has to mint a new one out-of-band. The lookup is by
// email to line up with the post-#446 login credential. The server should not
// be running concurrently against the same database.
func PromoteAdmin(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	email string,
) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("promote admin: %w", errPromoteEmailRequired)
	}

	dbc, err := config.ParseDatabase(getenv)
	if err != nil {
		return fmt.Errorf("promote admin: parse config: %w", err)
	}

	conn, err := setupDB(ctx, dbc, logger)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", cerr))
		}
	}()

	players := store.NewPlayerStore(conn, logger)
	player, err := players.GetPlayerByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			return fmt.Errorf("promote admin: %w (%q)", errPromoteEmailNotFound, email)
		}

		return fmt.Errorf("promote admin: %w", err)
	}

	if err := players.SetPlayerRole(ctx, player.ID, auth.RoleAdmin); err != nil {
		return fmt.Errorf("promote admin: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "Promoted %q to admin.\n", email); err != nil {
		return fmt.Errorf("promote admin: write confirmation: %w", err)
	}
	logger.InfoContext(ctx, "promoted to admin", slog.String("email", email))

	return nil
}

// SeedDemo seeds the demo baseline (the shared demo Host and the demo quiz)
// against the configured database. It exits early with an error if demo mode
// is not enabled so it cannot accidentally seed a non-demo DB.
func SeedDemo(ctx context.Context, getenv func(string) string, stderr io.Writer) error { // DEMO MODE
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if !demo.Enabled() {
		return fmt.Errorf("seed-demo: %w", errSeedDemoDisabled)
	}

	cfg, err := config.Parse(getenv)
	if err != nil {
		return fmt.Errorf("seed-demo: parse config: %w", err)
	}

	conn, err := setupDB(ctx, cfg.DatabaseConfig(), logger)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", cerr))
		}
	}()

	stores := store.New(conn, logger)
	mediaSvc := media.NewService(stores.Media, cfg.MediaDir, cfg.MediaImageMaxBytes, cfg.MediaAudioMaxBytes, logger)

	if err := demo.SeedIfEnabled(ctx, cfg, stores, mediaSvc, logger); err != nil {
		return fmt.Errorf("seed-demo: %w", err)
	}

	return nil
}

// readNewPassword prompts for a new password twice (input + confirmation)
// and returns the password if the two reads match and the value falls within
// the [auth.MinPasswordLength] / [auth.MaxPasswordLength] range. Length is
// validated *before* the second prompt so a too-short or too-long password
// fails fast with a single typed line - same UX as `passwd(1)`.
func readNewPassword(stdin io.Reader, stdout io.Writer) (string, error) {
	readPassword := newPasswordReader(stdin, stdout)

	password, err := readPassword("New password: ")
	if err != nil {
		return "", fmt.Errorf(resetWrap, err)
	}
	if len(password) < auth.MinPasswordLength {
		return "", fmt.Errorf(
			"reset password: %w (need at least %d characters)",
			errResetPasswordTooShort,
			auth.MinPasswordLength,
		)
	}
	if len(password) > auth.MaxPasswordLength {
		return "", fmt.Errorf(
			"reset password: %w (bcrypt accepts at most %d bytes)",
			errResetPasswordTooLong,
			auth.MaxPasswordLength,
		)
	}
	confirm, err := readPassword("Confirm password: ")
	if err != nil {
		return "", fmt.Errorf(resetWrap, err)
	}
	if confirm != password {
		return "", fmt.Errorf(resetWrap, errResetPasswordsDontMatch)
	}

	return password, nil
}

// newPasswordReader returns a per-call password reader sharing one
// scanner across the New/Confirm prompts (a fresh Scanner per call
// would buffer-ahead and leave the second read empty). On a TTY it
// uses [term.ReadPassword] so echo is disabled; otherwise it reads
// one scanner line per call.
func newPasswordReader(stdin io.Reader, stdout io.Writer) func(prompt string) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return func(prompt string) (string, error) {
			if _, err := fmt.Fprint(stdout, prompt); err != nil {
				return "", fmt.Errorf("write prompt: %w", err)
			}
			raw, err := term.ReadPassword(int(f.Fd()))
			if err != nil {
				return "", fmt.Errorf("read password: %w", err)
			}
			// Best-effort newline so the next prompt or log line starts on
			// a fresh row; a write failure here only mis-positions the
			// terminal cursor - the password has already been captured, so
			// returning an error would discard valid input.
			_, _ = fmt.Fprintln(stdout)

			return string(raw), nil
		}
	}

	scanner := bufio.NewScanner(stdin)

	return func(prompt string) (string, error) {
		if _, err := fmt.Fprint(stdout, prompt); err != nil {
			return "", fmt.Errorf("write prompt: %w", err)
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("read password: %w", err)
			}

			return "", fmt.Errorf("read password: %w", errResetEmptyInput)
		}

		return scanner.Text(), nil
	}
}

// errHealthcheckUnhealthy is wrapped by [Healthcheck] when /healthz
// returns a non-2xx status; defined at package scope so callers can
// [errors.Is] it without string-matching.
var errHealthcheckUnhealthy = errors.New("healthcheck unhealthy")

// Healthcheck probes http://127.0.0.1:$PORT/healthz on the local
// listener and returns nil iff the response is 2xx. Designed for the
// Dockerfile HEALTHCHECK directive (#344) so distroless images can
// run the existing server binary as their probe instead of carrying a
// separate wget / curl.
//
// Reads PORT from the environment so the probe targets whatever
// listener the server actually bound (the image defaults to 8080).
// Uses 127.0.0.1 rather than HOST to keep the probe loopback-only --
// HOST is the bind interface, not the right address to dial.
func Healthcheck(ctx context.Context, getenv func(string) string) error {
	port := getenv("PORT")
	if port == "" {
		port = config.PortDefault
	}

	const (
		healthcheckTimeout = 3 * time.Second
		statusOKMin        = 200
		statusOKMax        = 300
	)
	probeCtx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("build healthcheck request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < statusOKMin || resp.StatusCode >= statusOKMax {
		return fmt.Errorf("%w: status %d", errHealthcheckUnhealthy, resp.StatusCode)
	}

	return nil
}

// Check validates that the server can start: it parses config, opens the
// database, and runs migrations, then closes the connection and returns. No
// TCP listener is bound. Used by the `make smoke` target so a contributor
// can confirm the binary boots cleanly against the existing dev DB without
// process juggling.
func Check(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg, err := config.Parse(getenv)
	if err != nil {
		msg := "error parsing config"
		logger.ErrorContext(ctx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}
	logConfigSummary(ctx, logger, cfg)

	conn, err := setupDB(ctx, cfg.DatabaseConfig(), logger)
	if err != nil {
		return err
	}
	if cerr := conn.Close(); cerr != nil {
		logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", cerr))

		return fmt.Errorf("error closing database connection: %w", cerr)
	}

	logger.InfoContext(ctx, "startup ok")

	return nil
}
