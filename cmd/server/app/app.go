// Package app contains the main entrypoint for the server.
package app

import (
	"bufio"
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
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Sentinel errors for ResetPassword; defined at package level so err113 stays
// quiet while keeping the failure modes typed for callers and tests. Tests
// in the external app_test package match on these via [errors.Is]; see
// export_test.go for the re-exports.
var (
	errResetUsernameRequired   = errors.New("username is required")
	errResetPasswordTooShort   = errors.New("password too short")
	errResetPasswordTooLong    = errors.New("password too long")
	errResetUserNotFound       = errors.New("username not found")
	errResetEmptyInput         = errors.New("empty password input")
	errResetPasswordsDontMatch = errors.New("passwords do not match")
)

// resetWrap is the error-wrap prefix used by every ResetPassword failure
// path so error messages stay consistent and revive's add-constant linter
// stays quiet.
const resetWrap = "reset password: %w"

// ResetPassword reads a new password from stdin and overwrites the
// password_hash for the row identified by username. Operator-only tool for
// cases where an admin password is lost and the only alternative would be
// dropping the database volume.
//
// stdout carries interactive prompts only ("New password: ", "Confirm
// password: "); stderr carries structured slog output. Splitting them lets
// scripts redirect logs without capturing prompt noise (e.g. piping the new
// password in and discarding 2>/dev/null).
//
// stdin echo is disabled when stdin is a terminal; otherwise the password
// is read up to the first newline (so scripts can pipe). The supplied
// password must satisfy [auth.MinPasswordLength] and [auth.MaxPasswordLength].
//
// Order of operations is deliberately: parse config → open DB → look up
// username → THEN prompt. The lookup-before-prompt avoids making the
// operator type the password twice only to find out the username was a
// typo. There is a small TOCTOU window between the lookup and the UPDATE,
// which is acceptable for an operator-only tool that should not run while
// the server is live.
func ResetPassword(
	ctx context.Context,
	getenv func(string) string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	username string,
) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf(resetWrap, errResetUsernameRequired)
	}

	cfg, err := config.Parse(getenv)
	if err != nil {
		return fmt.Errorf("reset password: parse config: %w", err)
	}

	conn, err := setupDB(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", cerr))
		}
	}()

	players := store.NewPlayerStore(conn, logger)
	if _, lookupErr := players.GetPlayerByUsername(ctx, username); lookupErr != nil {
		if errors.Is(lookupErr, auth.ErrPlayerNotFound) {
			return fmt.Errorf("reset password: %w (%q)", errResetUserNotFound, username)
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

	if err := players.SetPlayerPasswordHash(ctx, username, hashed); err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			return fmt.Errorf("reset password: %w (%q)", errResetUserNotFound, username)
		}

		return fmt.Errorf(resetWrap, err)
	}

	logger.InfoContext(ctx, "password reset", slog.String("username", username))

	return nil
}

// readNewPassword prompts for a new password twice (input + confirmation)
// and returns the password if the two reads match and the value falls within
// the [auth.MinPasswordLength] / [auth.MaxPasswordLength] range. Length is
// validated *before* the second prompt so a too-short or too-long password
// fails fast with a single typed line — same UX as `passwd(1)`.
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

// newPasswordReader returns a closure that reads a password from stdin once
// per call. State (the [bufio.Scanner] for non-TTY input) is captured in the
// closure so successive reads advance through the same input stream — needed
// for the "New password: ... Confirm password: ..." sequence in
// ResetPassword. A fresh Scanner per call would buffer-ahead on the first
// call and leave the second with an empty reader.
//
// TTY path uses [term.ReadPassword] directly with the *[os.File] so echo is
// disabled; non-TTY path reads one scanner line per call. Both branches
// write the prompt to stdout so scripts piping a password in can still see
// (or 2>/dev/null discard) prompt text uniformly. TTY detection only works
// when stdin is a *[os.File]; wrapped readers always take the scanner path
// (today's only caller is cmd/server/main.go which passes [os.Stdin]).
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
			// terminal cursor — the password has already been captured, so
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

	conn, err := setupDB(ctx, cfg, logger)
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

// Run starts the application server, connects to the database, runs migrations, and listens for incoming requests.
func Run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
	ln net.Listener,
) error {
	var err error
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg, err := config.Parse(getenv)
	if err != nil {
		msg := "error parsing config"
		logger.ErrorContext(signalCtx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	conn, err := setupDB(signalCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		conErr := conn.Close()
		if conErr != nil {
			logger.ErrorContext(signalCtx, "error closing database connection", slog.Any("err", conErr))
		}
	}()

	stores := store.New(conn, logger)
	gameService := game.NewService(stores.Games, stores.Quizzes, logger)
	// Process-local pub/sub for the leaderboard SSE stream (#239). The
	// same hub is handed to the game service (publisher) and the server
	// (subscriber side) so submitted answers fan out to live viewers.
	leaderboardHub := leaderboard.NewHub()
	gameService.SetLeaderboardPublisher(leaderboardHub)

	srv := server.New(logger, stores, gameService, leaderboardHub, cfg)
	if ln == nil {
		ln, err = listener(signalCtx, cfg, logger)
		if err != nil {
			return fmt.Errorf("error creating listener: %w", err)
		}
	} else {
		logger.InfoContext(signalCtx, "listener overridden")
	}

	return runHTTPServer(ctx, signalCtx, ln, srv, logger)
}

func runHTTPServer(ctx, signalCtx context.Context, ln net.Listener, srv http.Handler, logger *slog.Logger) error {
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		Handler:           srv,
	}

	g, gCtx := errgroup.WithContext(signalCtx)

	g.Go(func() error {
		logger.InfoContext(gCtx, "listening on "+ln.Addr().String(), slog.String("addr", ln.Addr().String()))
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/admin/quizzes to manage quizzes", ln.Addr().String()))
		httpErr := httpServer.Serve(ln)
		if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
			msg := "error listening and serving"
			logger.ErrorContext(signalCtx, msg, slog.Any("err", httpErr))

			return fmt.Errorf("%v: %w", msg, httpErr)
		}

		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		// make a new context for the Shutdown
		// use the root ctx to ensure shutdown has its own timeout even though signalCtx is already canceled
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
		defer shutdownCancel()
		if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
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

func setupDB(signalCtx context.Context, cfg *config.Config, logger *slog.Logger) (*sql.DB, error) {
	conn, err := database.Open(
		signalCtx,
		cfg.DBDriver,
		cfg.DBURI,
		cfg.DBMaxOpenConns,
		cfg.DBMaxIdleConns,
		cfg.DBConnMaxLifetime,
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
