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

// Sentinel errors for the password-prompt commands; defined at package level
// so err113 stays quiet while keeping the failure modes typed for callers and
// tests. Tests in the external app_test package match on these via [errors.Is];
// see export_test.go for the re-exports. The errPassword* set is shared by
// ResetPassword and CreateAdmin via [readNewPassword].
var (
	errResetEmailRequired = errors.New("email is required")
	errResetUserNotFound  = errors.New("email not found")
	errPasswordTooShort   = errors.New("password too short")
	errPasswordTooLong    = errors.New("password too long")
	errEmptyInput         = errors.New("empty password input")
	errPasswordsDontMatch = errors.New("passwords do not match")
)

// resetWrap is the error-wrap prefix used by every ResetPassword failure
// path so error messages stay consistent and revive's add-constant linter
// stays quiet.
const resetWrap = "reset password: %w"

// opWrap wraps an error under a runtime operation label; keeps revive's
// add-constant linter quiet across its several uses.
const opWrap = "%s: %w"

// emailKey is the slog attribute key the admin commands log the target email
// under; a shared const keeps revive's add-constant linter quiet.
const emailKey = "email"

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

	password, err := readNewPassword(stdin, stdout, "reset password")
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

	logger.InfoContext(ctx, "password reset", slog.String(emailKey, email))

	return nil
}

// errPromoteEmailRequired is wrapped by [PromoteAdmin] when the
// supplied email trims to empty; defined at package scope so callers
// and tests can match it via [errors.Is].
var errPromoteEmailRequired = errors.New("email is required")

// errPromoteEmailNotFound is wrapped by [PromoteAdmin] when no player row
// matches the supplied email.
var errPromoteEmailNotFound = errors.New("email not found")

// errVerifyEmailRequired is wrapped by [VerifyEmail] when the supplied email
// trims to empty; defined at package scope so callers and tests can match it
// via [errors.Is].
var errVerifyEmailRequired = errors.New("email is required")

// errVerifyEmailNotFound is wrapped by [VerifyEmail] when no player row
// matches the supplied email.
var errVerifyEmailNotFound = errors.New("email not found")

// errSeedDemoDisabled is returned by SeedDemo when demo mode (Config.DemoMode,
// from DEMO_MODE_ENABLED) is off; defined at package scope so callers can match
// it via [errors.Is].
var errSeedDemoDisabled = errors.New("DEMO_MODE_ENABLED is not set")

// errSeedDemoArchiveNotSet is returned by SeedDemo when DEMO_SEED_ARCHIVE_DIR is
// empty; defined at package scope so callers can match it via [errors.Is].
var errSeedDemoArchiveNotSet = errors.New("DEMO_SEED_ARCHIVE_DIR is not set")

// seedDemoWrap is the error-wrap prefix used by SeedDemo failure paths so the
// messages stay consistent.
const seedDemoWrap = "seed-demo: %w"

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
	logger.InfoContext(ctx, "promoted to admin", slog.String(emailKey, email))

	return nil
}

// VerifyEmail stamps email_verified_at for the player with the given email, the
// break-glass path to verify an account when no SMTP is configured to send the
// mailed link. The server should not be running concurrently against the same
// database.
func VerifyEmail(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	email string,
) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("verify email: %w", errVerifyEmailRequired)
	}

	dbc, err := config.ParseDatabase(getenv)
	if err != nil {
		return fmt.Errorf("verify email: parse config: %w", err)
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
			return fmt.Errorf("verify email: %w (%q)", errVerifyEmailNotFound, email)
		}

		return fmt.Errorf("verify email: %w", err)
	}

	if err := players.SetPlayerEmailVerifiedNow(ctx, player.ID); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "Verified email for %q.\n", email); err != nil {
		return fmt.Errorf("verify email: write confirmation: %w", err)
	}
	logger.InfoContext(ctx, "email verified", slog.String(emailKey, email))

	return nil
}

// errCreateAdminEmailRequired is wrapped by [CreateAdmin] when the supplied
// email trims to empty.
var errCreateAdminEmailRequired = errors.New("email is required")

// errCreateAdminEmailExists is wrapped by [CreateAdmin] when a player already
// owns the email; the operator should use -promote-admin / -verify-email
// instead.
var errCreateAdminEmailExists = errors.New("email already exists")

// errCreateAdminInvalidEmail is wrapped by [CreateAdmin] when the supplied
// email fails the [auth.LooksLikeEmail] shape check, so a malformed address
// never becomes a verified admin that later breaks the email-change flows.
var errCreateAdminInvalidEmail = errors.New("invalid email")

// createAdminWrap is the error-wrap prefix shared by [CreateAdmin] failure
// paths so revive's add-constant linter stays quiet.
const createAdminWrap = "create admin: %w"

// CreateAdmin creates a verified admin account whose password is read from stdin.
func CreateAdmin(
	ctx context.Context,
	getenv func(string) string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	email string,
) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf(createAdminWrap, errCreateAdminEmailRequired)
	}
	if !auth.LooksLikeEmail(email) {
		return fmt.Errorf("create admin: %w (%q)", errCreateAdminInvalidEmail, email)
	}

	dbc, err := config.ParseDatabase(getenv)
	if err != nil {
		return fmt.Errorf("create admin: parse config: %w", err)
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
	switch _, lookupErr := players.GetPlayerByEmail(ctx, email); {
	case lookupErr == nil:
		return errCreateAdminEmailExistsFor(email)
	case !errors.Is(lookupErr, auth.ErrPlayerNotFound):
		return fmt.Errorf(createAdminWrap, lookupErr)
	}

	password, err := readNewPassword(stdin, stdout, "create admin")
	if err != nil {
		return err
	}

	hashed, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("create admin: hash password: %w", err)
	}

	player, err := createVerifiedAdmin(ctx, players, email, hashed)
	if err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			return errCreateAdminEmailExistsFor(email)
		}

		return err
	}

	if _, err := fmt.Fprintf(stdout, "Created admin %q (display name %q).\n", email, player.DisplayName); err != nil {
		return fmt.Errorf("create admin: write confirmation: %w", err)
	}
	logger.InfoContext(ctx, "created admin",
		slog.String(emailKey, email), slog.String("display_name", player.DisplayName))

	return nil
}

// createVerifiedAdmin inserts a verified admin row in one write. It first tries
// the email's local-part display name, then falls back to petnames for a bounded
// number of attempts when the name is already taken.
func createVerifiedAdmin(
	ctx context.Context, players *store.PlayerStore, email, passwordHash string,
) (*auth.Player, error) {
	player, err := auth.CreateWithPetnameFallback(
		displayNameFromEmail(email),
		func(name string) (*auth.Player, error) {
			return players.CreatePlayerByAdmin(ctx, name, email, passwordHash, auth.RoleAdmin)
		},
	)
	if err != nil {
		return nil, fmt.Errorf(createAdminWrap, err)
	}

	return player, nil
}

// errCreateAdminEmailExistsFor wraps errCreateAdminEmailExists with the offending
// email and the recovery guidance, shared by the pre-check and the insert race.
func errCreateAdminEmailExistsFor(email string) error {
	return fmt.Errorf(
		"create admin: %w (%q); use -promote-admin or -verify-email instead",
		errCreateAdminEmailExists, email,
	)
}

// displayNameFromEmail derives a display name from an email's local part,
// capping it at [auth.MaxDisplayNameLength] runes and falling back to a
// [auth.GeneratePetname] when the local part is empty.
func displayNameFromEmail(email string) string {
	local, _, _ := strings.Cut(email, "@")
	local = strings.TrimSpace(local)
	if runes := []rune(local); len(runes) > auth.MaxDisplayNameLength {
		local = string(runes[:auth.MaxDisplayNameLength])
	}
	if local == "" {
		return auth.GeneratePetname()
	}

	return local
}

// errInitialAdminInvalidEmail is wrapped by [bootstrapInitialAdmin] when
// INITIAL_ADMIN_EMAIL fails the [auth.LooksLikeEmail] shape check, so a
// misconfigured operator fails startup instead of minting a broken admin.
var errInitialAdminInvalidEmail = errors.New("INITIAL_ADMIN_EMAIL is not a valid email")

// errInitialAdminPasswordTooShort is wrapped by [bootstrapInitialAdmin] when
// INITIAL_ADMIN_PASSWORD is below [auth.MinPasswordLength].
var errInitialAdminPasswordTooShort = errors.New("INITIAL_ADMIN_PASSWORD is too short")

// errInitialAdminPasswordTooLong is wrapped by [bootstrapInitialAdmin] when
// INITIAL_ADMIN_PASSWORD exceeds [auth.MaxPasswordLength] (bcrypt's 72-byte
// limit), so the operator sees a clear message instead of a cryptic hash error.
var errInitialAdminPasswordTooLong = errors.New("INITIAL_ADMIN_PASSWORD is too long")

// bootstrapWrap is the error-wrap prefix shared by [bootstrapInitialAdmin]
// failure paths so revive's add-constant linter stays quiet.
const bootstrapWrap = "bootstrap initial admin: %w"

// bootstrapInitialAdmin mints a verified admin from INITIAL_ADMIN_EMAIL /
// INITIAL_ADMIN_PASSWORD on first boot, and is a no-op once any admin exists.
func bootstrapInitialAdmin(
	ctx context.Context, cfg *config.Config, players *store.PlayerStore, logger *slog.Logger,
) error {
	email := strings.ToLower(strings.TrimSpace(cfg.InitialAdminEmail))
	password := cfg.InitialAdminPassword
	if email == "" || password == "" {
		return nil
	}

	hasAdmin, err := players.HasAnyAdmin(ctx)
	if err != nil {
		return fmt.Errorf(bootstrapWrap, err)
	}
	if hasAdmin {
		logger.InfoContext(ctx, "initial admin bootstrap skipped: an admin already exists")

		return nil
	}

	if !auth.LooksLikeEmail(email) {
		return fmt.Errorf("bootstrap initial admin: %w (%q)", errInitialAdminInvalidEmail, email)
	}
	if len(password) < auth.MinPasswordLength {
		return fmt.Errorf(
			"bootstrap initial admin: %w (need at least %d characters)",
			errInitialAdminPasswordTooShort, auth.MinPasswordLength,
		)
	}
	if len(password) > auth.MaxPasswordLength {
		return fmt.Errorf(
			"bootstrap initial admin: %w (bcrypt accepts at most %d bytes)",
			errInitialAdminPasswordTooLong, auth.MaxPasswordLength,
		)
	}

	switch _, lookupErr := players.GetPlayerByEmail(ctx, email); {
	case lookupErr == nil:
		logger.WarnContext(ctx, "initial admin bootstrap skipped: email already in use",
			slog.String(emailKey, email))

		return nil
	case !errors.Is(lookupErr, auth.ErrPlayerNotFound):
		return fmt.Errorf(bootstrapWrap, lookupErr)
	}

	hashed, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("bootstrap initial admin: hash password: %w", err)
	}

	player, err := createVerifiedAdmin(ctx, players, email, hashed)
	if err != nil {
		// Another instance won the race between our HasAnyAdmin check and this
		// insert; the admin now exists, so skip rather than fail this boot.
		if errors.Is(err, auth.ErrEmailTaken) {
			logger.InfoContext(ctx, "initial admin already created concurrently, skipping",
				slog.String(emailKey, email))

			return nil
		}

		return fmt.Errorf(bootstrapWrap, err)
	}

	logger.InfoContext(ctx, "initial admin created",
		slog.String(emailKey, email), slog.String("display_name", player.DisplayName))

	return nil
}

// SeedDemo seeds the demo baseline (the shared demo Host and the demo quiz set)
// against the configured database. It exits early with an error when demo mode
// is off (Config.DemoMode) so it cannot accidentally seed a non-demo DB. Every
// *.zip in the DEMO_SEED_ARCHIVE_DIR directory is restored as a demo quiz, in
// sorted-filename order; the archives are not embedded in the binary so the
// files stay out of the production image and are supplied by the demo
// deployment's bind mount instead.
func SeedDemo(ctx context.Context, getenv func(string) string, stderr io.Writer) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg, err := config.Parse(getenv)
	if err != nil {
		return fmt.Errorf("seed-demo: parse config: %w", err)
	}
	if !cfg.DemoMode {
		return fmt.Errorf(seedDemoWrap, errSeedDemoDisabled)
	}

	if cfg.DemoSeedArchiveDir == "" {
		return fmt.Errorf(seedDemoWrap, errSeedDemoArchiveNotSet)
	}

	archives, err := readDemoArchives(cfg.DemoSeedArchiveDir)
	if err != nil {
		return fmt.Errorf(seedDemoWrap, err)
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

	if err := demo.SeedIfEnabled(ctx, cfg, stores, mediaSvc, logger, archives); err != nil {
		return fmt.Errorf(seedDemoWrap, err)
	}

	return nil
}

// readDemoArchives reads every *.zip in dir into memory, one byte slice per
// archive, in the sorted-filename order [demo.ArchivePaths] returns. It errors
// with [demo.ErrNoArchives] when the directory holds no .zip files so a
// misconfigured mount fails fast.
func readDemoArchives(dir string) ([][]byte, error) {
	paths, err := demo.ArchivePaths(dir)
	if err != nil {
		return nil, fmt.Errorf("scan demo archives: %w", err)
	}

	archives := make([][]byte, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // operator-provided dir from a trusted env var
		if err != nil {
			return nil, fmt.Errorf("read archive %q: %w", path, err)
		}
		archives = append(archives, raw)
	}

	return archives, nil
}

// readNewPassword prompts for a new password twice (input + confirmation)
// and returns the password if the two reads match and the value falls within
// the [auth.MinPasswordLength] / [auth.MaxPasswordLength] range. Length is
// validated *before* the second prompt so a too-short or too-long password
// fails fast with a single typed line - same UX as `passwd(1)`. op labels
// the wrapping error prefix so both -reset-password and -create-admin reuse it.
func readNewPassword(stdin io.Reader, stdout io.Writer, op string) (string, error) {
	readPassword := newPasswordReader(stdin, stdout)

	password, err := readPassword("New password: ")
	if err != nil {
		return "", fmt.Errorf(opWrap, op, err)
	}
	if len(password) < auth.MinPasswordLength {
		return "", fmt.Errorf(
			"%s: %w (need at least %d characters)",
			op,
			errPasswordTooShort,
			auth.MinPasswordLength,
		)
	}
	if len(password) > auth.MaxPasswordLength {
		return "", fmt.Errorf(
			"%s: %w (bcrypt accepts at most %d bytes)",
			op,
			errPasswordTooLong,
			auth.MaxPasswordLength,
		)
	}
	confirm, err := readPassword("Confirm password: ")
	if err != nil {
		return "", fmt.Errorf(opWrap, op, err)
	}
	if confirm != password {
		return "", fmt.Errorf(opWrap, op, errPasswordsDontMatch)
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

			return "", fmt.Errorf("read password: %w", errEmptyInput)
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

		return fmt.Errorf(opWrap, msg, err)
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
