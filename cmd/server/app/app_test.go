package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	. "github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/store"
)

// TestMain wires goose's global state up once for the package - calling
// SetupGoose from parallel tests races on the goose package-level fields.
func TestMain(m *testing.M) {
	database.SetupGoose()
	m.Run()
}

func TestCheck_FreshDB_Succeeds(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		return map[string]string{"APP_ENV": "development", "DB_URI": dbURI, "PORT": "0"}[key]
	}

	var stdout bytes.Buffer
	if err := Check(t.Context(), getenv, &stdout); err != nil {
		t.Fatalf("Check err = %v, want nil", err)
	}
	if got, want := stdout.String(), "startup ok"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want substring %q", got, want)
	}
}

func TestCheck_BadDBURI_ReturnsError(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		// A path under a nonexistent directory: SQLite will fail to open it.
		return map[string]string{"DB_URI": "file:/nonexistent-dir/topbanana.sqlite", "PORT": "0"}[key]
	}

	var stdout bytes.Buffer
	err := Check(t.Context(), getenv, &stdout)
	if err == nil {
		t.Fatal("Check err = nil, want non-nil for unreachable DB_URI")
	}
}

// seedOldPassword is the value seedPlayer hashes into the row before each
// ResetPassword test runs. Tests assert the hash changed (or didn't) by
// re-checking this exact string against the on-disk hash afterwards, so the
// constant is shared instead of duplicated.
const seedOldPassword = "old-correct-battery"

// seedPlayer opens the dev DB at dbURI and inserts a player with the given
// username and the shared [seedOldPassword]. Returns the original hash so
// tests can assert it actually changed after ResetPassword.
func seedPlayer(t *testing.T, dbURI, username string) string {
	t.Helper()

	hashed, err := auth.HashPassword(seedOldPassword)
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}

	conn, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("conn.Close err = %v, want nil", cerr)
		}
	})
	if err := database.Migrate(conn); err != nil {
		t.Fatalf("Migrate err = %v, want nil", err)
	}

	players := store.NewPlayerStore(conn, slog.Default())
	if _, err := players.CreatePlayer(
		t.Context(),
		username,
		username+"@example.test",
		hashed,
		auth.RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	return hashed
}

// fetchSeededPlayer re-opens dbURI and returns the seeded "alice" row. Used
// by "no-write" assertions to confirm the on-disk hash was not overwritten
// when a failure path triggered before the UPDATE. The username is fixed
// because every call site asserts against the same seeded user; passing it
// in just made unparam noisy.
func fetchSeededPlayer(t *testing.T, dbURI string) *auth.Player {
	t.Helper()

	conn, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("conn.Close err = %v, want nil", cerr)
		}
	})
	players := store.NewPlayerStore(conn, slog.Default())
	p, err := players.GetPlayerByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}

	return p
}

// envFor returns a getenv-style closure binding DB_URI to the per-test DB and
// PORT to a sentinel. ResetPassword does not bind a listener, so PORT is
// only present so [config.Parse] is satisfied.
func envFor(dbURI string) func(string) string {
	return func(key string) string {
		return map[string]string{"APP_ENV": "development", "DB_URI": dbURI, "PORT": "0"}[key]
	}
}

func TestResetPassword_HappyPath_RotatesHash(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		username    = "alice"
		newPassword = "new-correct-battery"
	)
	originalHash := seedPlayer(t, dbURI, username)

	stdin := strings.NewReader(newPassword + "\n" + newPassword + "\n")
	var stdout, stderr bytes.Buffer
	if err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, username); err != nil {
		t.Fatalf("ResetPassword err = %v, want nil", err)
	}

	p := fetchSeededPlayer(t, dbURI)
	if got, want := p.PasswordHash, originalHash; got == want {
		t.Errorf("PasswordHash after ResetPassword = %q, want a value different from %q", got, want)
	}
	if err := auth.CheckPassword(p.PasswordHash, newPassword); err != nil {
		t.Errorf("CheckPassword(newPassword) err = %v, want nil - new hash should validate", err)
	}

	// Prompts must land on stdout, slog output must land on stderr -
	// scripts piping the password in rely on `2>/dev/null` discarding logs
	// without eating prompt text.
	stdoutStr := stdout.String()
	if want := "New password: "; !strings.Contains(stdoutStr, want) {
		t.Errorf("stdout = %q, want substring %q", stdoutStr, want)
	}
	if want := "Confirm password: "; !strings.Contains(stdoutStr, want) {
		t.Errorf("stdout = %q, want substring %q", stdoutStr, want)
	}
	if got, want := stderr.String(), "password reset"; !strings.Contains(got, want) {
		t.Errorf("stderr = %q, want substring %q", got, want)
	}
	if want := "password reset"; strings.Contains(stdoutStr, want) {
		t.Errorf("stdout = %q, want no occurrence of slog output %q", stdoutStr, want)
	}
}

// TestResetPassword_UsernameWhitespaceTrimmed_RotatesHash exercises the
// strings.TrimSpace defense in ResetPassword so callers cannot accidentally
// treat "alice" and " alice " as different rows. The store layer trims too,
// but covering it at the ResetPassword boundary locks in the contract.
func TestResetPassword_UsernameWhitespaceTrimmed_RotatesHash(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const newPassword = "new-correct-battery"
	originalHash := seedPlayer(t, dbURI, "alice")

	stdin := strings.NewReader(newPassword + "\n" + newPassword + "\n")
	var stdout, stderr bytes.Buffer
	if err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "  alice  "); err != nil {
		t.Fatalf("ResetPassword err = %v, want nil", err)
	}

	p := fetchSeededPlayer(t, dbURI)
	if got, want := p.PasswordHash, originalHash; got == want {
		t.Errorf("PasswordHash after ResetPassword = %q, want a value different from %q", got, want)
	}
	if err := auth.CheckPassword(p.PasswordHash, newPassword); err != nil {
		t.Errorf("CheckPassword(newPassword) err = %v, want nil", err)
	}
}

func TestResetPassword_UnknownUsername_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	// Apply migrations so the players table exists.
	seedPlayer(t, dbURI, "someone-else")

	stdin := strings.NewReader("new-correct-battery\nnew-correct-battery\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "ghost")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for unknown username")
	}
	if got, want := err, ErrResetUserNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	// The user-not-found check fires before any prompt, so stdout should
	// remain empty - no point asking for a password we will never use.
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty (unknown username should fail before prompting)", got)
	}
}

func TestResetPassword_PasswordTooShort_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	stdin := strings.NewReader("short\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for too-short password")
	}
	if got, want := err, ErrResetPasswordTooShort; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	p := fetchSeededPlayer(t, dbURI)
	if err := auth.CheckPassword(p.PasswordHash, seedOldPassword); err != nil {
		t.Errorf("original password should still validate; the rejection happened before any DB write, err = %v", err)
	}
}

// TestResetPassword_PasswordTooLong_ReturnsError covers the bcrypt 72-byte
// cap. Without an upfront check, [bcrypt.GenerateFromPassword] either
// truncates silently or returns ErrPasswordTooLong depending on lib version
// - surfacing it as a typed sentinel keeps the operator-facing message
// stable across upgrades.
func TestResetPassword_PasswordTooLong_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	tooLong := strings.Repeat("a", auth.MaxPasswordLength+1)
	stdin := strings.NewReader(tooLong + "\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for too-long password")
	}
	if got, want := err, ErrResetPasswordTooLong; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	p := fetchSeededPlayer(t, dbURI)
	if err := auth.CheckPassword(p.PasswordHash, seedOldPassword); err != nil {
		t.Errorf("original password should still validate after too-long rejection, err = %v", err)
	}
}

func TestResetPassword_ConfirmationMismatch_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	// Two lines, both long enough to pass the length check, but different.
	stdin := strings.NewReader("new-correct-battery\nnew-correct-typo-here\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for mismatching confirmation")
	}
	if got, want := err, ErrResetPasswordsDontMatch; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	p := fetchSeededPlayer(t, dbURI)
	if err := auth.CheckPassword(p.PasswordHash, seedOldPassword); err != nil {
		t.Errorf("original password should still validate after confirmation mismatch, err = %v", err)
	}
}

// TestResetPassword_EmptyUsername_ReturnsError covers the up-front guard:
// an empty (or whitespace-only) username should fail before any config
// parse or DB open, so the test passes a getenv that would itself error
// to confirm the guard fires first.
func TestResetPassword_EmptyUsername_ReturnsError(t *testing.T) {
	t.Parallel()

	// Intentionally bogus env: if the empty-username guard didn't fire
	// first, config.Parse would hit this and we'd see a different error.
	getenv := func(string) string { return "" }

	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), getenv, strings.NewReader(""), &stdout, &stderr, "   ")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for whitespace-only username")
	}
	if got, want := err, ErrResetUsernameRequired; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty (guard should fire before any prompt)", got)
	}
}

// TestResetPassword_ClosedStdin_ReturnsError covers the non-TTY scanner
// branch when the operator pipes nothing in (e.g. `</dev/null`). Without the
// errResetEmptyInput sentinel we'd surface a generic "read password" error;
// the typed sentinel lets the test pin the contract.
func TestResetPassword_ClosedStdin_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), strings.NewReader(""), &stdout, &stderr, "alice")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for empty stdin")
	}
	if got, want := err, ErrResetEmptyInput; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	p := fetchSeededPlayer(t, dbURI)
	if err := auth.CheckPassword(p.PasswordHash, seedOldPassword); err != nil {
		t.Errorf("original password should still validate after empty stdin, err = %v", err)
	}
}

// stubVerifySweep records how many DeleteExpiredVerifyTokens calls
// landed and optionally returns an error on each call. Concurrent-safe
// because the sweep goroutine and the test assert on the counter from
// different goroutines.
type stubVerifySweep struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubVerifySweep) DeleteExpiredVerifyTokens(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return s.err
}

func (s *stubVerifySweep) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// stubResetSweep mirrors stubVerifySweep for the reset side.
type stubResetSweep struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubResetSweep) DeleteExpiredResetTokens(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return s.err
}

func (s *stubResetSweep) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// TestRunTokenSweep_TicksUntilCancel pins the loop's two contracts:
// each tick calls both DeleteExpired* methods, and a context cancel
// returns the goroutine promptly. A short interval keeps the test
// fast; the production wiring uses an hour.
func TestRunTokenSweep_TicksUntilCancel(t *testing.T) {
	t.Parallel()

	verify := &stubVerifySweep{}
	reset := &stubResetSweep{}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		RunTokenSweep(ctx, slog.New(slog.DiscardHandler), verify, reset, time.Millisecond)
		close(done)
	}()

	// Wait until at least one tick lands on each store before cancelling.
	deadline := time.After(time.Second)
	for verify.Calls() <= 0 || reset.Calls() <= 0 {
		select {
		case <-deadline:
			t.Fatalf("sweep did not tick; verify=%d reset=%d", verify.Calls(), reset.Calls())
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep did not return after cancel")
	}
}

// TestRunTokenSweep_ContinuesAfterError pins that a single sweep
// failure does not silence the loop: the warn-and-continue path keeps
// the next tick alive so a transient DB error does not stop expiry
// cleanup until the next deploy.
func TestRunTokenSweep_ContinuesAfterError(t *testing.T) {
	t.Parallel()

	verify := &stubVerifySweep{err: errors.New("verify sweep failed")}
	reset := &stubResetSweep{err: errors.New("reset sweep failed")}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		RunTokenSweep(ctx, slog.New(slog.DiscardHandler), verify, reset, time.Millisecond)
		close(done)
	}()

	// Wait for at least two ticks per store so the "continue past error"
	// invariant is observable.
	deadline := time.After(time.Second)
	for verify.Calls() < 2 || reset.Calls() < 2 {
		select {
		case <-deadline:
			t.Fatalf("sweep did not tick twice; verify=%d reset=%d", verify.Calls(), reset.Calls())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

// TestBuildMailer_WarnsWhenSMTPConfiguredAndBaseURLEmpty pins the
// boot-time WARN log that surfaces the silent-no-op trap: when SMTP
// is wired but BASE_URL is empty, every email dispatcher silently
// drops its send. The diagnostics page also surfaces this, but the
// log line catches it in the boot transcript so a deploy that goes
// straight to "running" without a human visiting /admin/email still
// gets a visible signal (#495).
func TestBuildMailer_WarnsWhenSMTPConfiguredAndBaseURLEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := &config.Config{
		SMTPHost: "mailpit",
		SMTPPort: 1025,
		SMTPFrom: "topbanana@localhost",
		SMTPTLS:  false,
		BaseURL:  "",
	}

	_, status, err := BuildMailer(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("BuildMailer err = %v, want nil", err)
	}
	if got, want := status.Configured, true; got != want {
		t.Errorf("status.Configured = %v, want %v", got, want)
	}
	if got, want := buf.String(), "email links disabled: BASE_URL is unset while SMTP is configured"; !strings.Contains(
		got,
		want,
	) {
		t.Errorf("log output = %q, should contain %q", got, want)
	}
	if got, want := buf.String(), "level=WARN"; !strings.Contains(got, want) {
		t.Errorf("log output = %q, should contain %q (WARN, not INFO)", got, want)
	}
}

// TestBuildMailer_NoWarnWhenSMTPConfiguredAndBaseURLSet pins the
// quiet path: a deploy that wires both SMTP and BASE_URL should not
// emit the email-links-disabled warning, otherwise the boot log
// would cry wolf on every healthy production restart.
func TestBuildMailer_NoWarnWhenSMTPConfiguredAndBaseURLSet(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := &config.Config{
		SMTPHost: "mailpit",
		SMTPPort: 1025,
		SMTPFrom: "topbanana@localhost",
		BaseURL:  "https://quiz.example.test",
	}

	_, status, err := BuildMailer(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("BuildMailer err = %v, want nil", err)
	}
	if got, want := status.BaseURL, "https://quiz.example.test"; got != want {
		t.Errorf("status.BaseURL = %q, want %q", got, want)
	}
	if strings.Contains(buf.String(), "email links disabled") {
		t.Errorf("log output = %q, should not contain email-links-disabled warning when BaseURL is set", buf.String())
	}
}

// TestBuildMailer_NoWarnWhenSMTPUnconfigured pins the unconfigured
// path: a deploy with no SMTP at all shouldn't be lectured about
// BASE_URL too. The unconfigured info line already explains why no
// email leaves the box; piling another warning on top would be noise.
func TestBuildMailer_NoWarnWhenSMTPUnconfigured(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := &config.Config{BaseURL: ""}

	_, status, err := BuildMailer(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("BuildMailer err = %v, want nil", err)
	}
	if got, want := status.Configured, false; got != want {
		t.Errorf("status.Configured = %v, want %v", got, want)
	}
	if strings.Contains(buf.String(), "email links disabled") {
		t.Errorf(
			"log output = %q, should not contain email-links-disabled warning when SMTP is unconfigured",
			buf.String(),
		)
	}
}
