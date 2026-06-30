package app_test

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	. "github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/store"
)

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

// seedOldPassword is the value seedPlayer hashes into the row before each
// ResetPassword test runs. Tests assert the hash changed (or didn't) by
// re-checking this exact string against the on-disk hash afterwards, so the
// constant is shared instead of duplicated.
const seedOldPassword = "old-correct-battery"

// seedPlayer opens the dev DB at dbURI and inserts a player with the given
// displayName and the shared [seedOldPassword]. Returns the original hash so
// tests can assert it actually changed after ResetPassword.
func seedPlayer(t *testing.T, dbURI, displayName string) string {
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
		displayName,
		displayName+"@example.test",
		hashed,
		auth.RolePlayer,
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	return hashed
}

// fetchSeededPlayer re-opens dbURI and returns the seeded "alice" row. Used
// by "no-write" assertions to confirm the on-disk hash was not overwritten
// when a failure path triggered before the UPDATE. The displayName is fixed
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
	p, err := players.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
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

// minimalEnvFor returns a getenv-style closure that exposes ONLY DB_URI -
// no APP_ENV, no SESSION_KEY, no SMTP/OAuth. This is the locked-out
// recovery scenario the break-glass tools must run in: config.Parse would
// reject it (production-secure defaults require SESSION_KEY when APP_ENV is
// unset), but config.ParseDatabase resolves just the DB so the tools work.
func minimalEnvFor(dbURI string) func(string) string {
	return func(key string) string {
		if key == "DB_URI" {
			return dbURI
		}

		return ""
	}
}

// TestPromoteAdmin_MinimalEnv_NoSessionKey pins the break-glass contract:
// PromoteAdmin succeeds with only DB_URI set (no APP_ENV, no SESSION_KEY),
// which config.Parse would reject. The role change must actually land.
func TestPromoteAdmin_MinimalEnv_NoSessionKey(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		displayName = "alice"
		email       = "alice@example.test"
	)
	seedPlayer(t, dbURI, displayName)
	demoteToHost(t, dbURI, displayName)

	var stdout, stderr bytes.Buffer
	if err := PromoteAdmin(t.Context(), minimalEnvFor(dbURI), &stdout, &stderr, email); err != nil {
		t.Fatalf("PromoteAdmin err = %v, want nil with only DB_URI set", err)
	}

	p := fetchSeededPlayer(t, dbURI)
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role after PromoteAdmin = %q, want %q", got, want)
	}
}

// TestResetPassword_MinimalEnv_NoSessionKey is the ResetPassword half of
// the break-glass contract: a password reset succeeds with only DB_URI set.
func TestResetPassword_MinimalEnv_NoSessionKey(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		displayName = "alice"
		email       = "alice@example.test"
		newPassword = "new-correct-battery"
	)
	originalHash := seedPlayer(t, dbURI, displayName)

	stdin := strings.NewReader(newPassword + "\n" + newPassword + "\n")
	var stdout, stderr bytes.Buffer
	if err := ResetPassword(t.Context(), minimalEnvFor(dbURI), stdin, &stdout, &stderr, email); err != nil {
		t.Fatalf("ResetPassword err = %v, want nil with only DB_URI set", err)
	}

	p := fetchSeededPlayer(t, dbURI)
	if got, want := p.PasswordHash, originalHash; got == want {
		t.Errorf("PasswordHash after ResetPassword = %q, want a value different from %q", got, want)
	}
	if err := auth.CheckPassword(p.PasswordHash, newPassword); err != nil {
		t.Errorf("CheckPassword(newPassword) err = %v, want nil", err)
	}
}

func TestResetPassword_HappyPath_RotatesHash(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		displayName = "alice"
		email       = "alice@example.test"
		newPassword = "new-correct-battery"
	)
	originalHash := seedPlayer(t, dbURI, displayName)

	stdin := strings.NewReader(newPassword + "\n" + newPassword + "\n")
	var stdout, stderr bytes.Buffer
	if err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, email); err != nil {
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

// TestResetPassword_EmailWhitespaceAndCaseNormalized_RotatesHash exercises
// the strings.TrimSpace + ToLower normalisation in ResetPassword so callers
// cannot accidentally treat "alice@example.test" and " ALICE@example.test "
// as different rows. The store layer normalises too, but covering it at the
// ResetPassword boundary locks in the contract.
func TestResetPassword_EmailWhitespaceAndCaseNormalized_RotatesHash(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const newPassword = "new-correct-battery"
	originalHash := seedPlayer(t, dbURI, "alice")

	stdin := strings.NewReader(newPassword + "\n" + newPassword + "\n")
	var stdout, stderr bytes.Buffer
	if err := ResetPassword(
		t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "  ALICE@Example.test  ",
	); err != nil {
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

func TestPromoteAdmin_HappyPath_SetsAdminRole(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		displayName = "alice"
		email       = "alice@example.test"
	)
	// Seed two players: the first credentialled registrant becomes Admin
	// automatically, so the SECOND ("bob", a Player) is the meaningful
	// promote target. We still promote alice's row by displayName via the
	// fixed-"alice" fetch helper; demote it to Host first so the promote is
	// a real transition rather than a no-op.
	seedPlayer(t, dbURI, displayName)
	demoteToHost(t, dbURI, displayName)

	var stdout, stderr bytes.Buffer
	if err := PromoteAdmin(t.Context(), envFor(dbURI), &stdout, &stderr, email); err != nil {
		t.Fatalf("PromoteAdmin err = %v, want nil", err)
	}

	p := fetchSeededPlayer(t, dbURI)
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role after PromoteAdmin = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "Promoted"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want substring %q", got, want)
	}
}

func TestPromoteAdmin_UnknownEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := PromoteAdmin(t.Context(), envFor(dbURI), &stdout, &stderr, "nobody@example.test")
	if got, want := err, ErrPromoteEmailNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestPromoteAdmin_BlankEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := PromoteAdmin(t.Context(), envFor(dbURI), &stdout, &stderr, "   ")
	if got, want := err, ErrPromoteEmailRequired; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// TestSeedDemo_DisabledMode_ReturnsError pins the guard: SeedDemo refuses to
// seed when DEMO_MODE_ENABLED is off, so it can never populate a non-demo DB.
// The guard runs before any DB access, so no test DB is needed. Cannot use
// t.Parallel because it mutates the process environment via t.Setenv;
// demo.Enabled() reads os.Getenv directly.
//
//nolint:paralleltest // t.Setenv + t.Parallel are incompatible.
func TestSeedDemo_DisabledMode_ReturnsError(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "")

	var stderr bytes.Buffer
	err := SeedDemo(t.Context(), func(string) string { return "" }, &stderr)
	if got, want := err, ErrSeedDemoDisabled; !errors.Is(got, want) {
		t.Errorf("SeedDemo err = %v, want %v", got, want)
	}
}

// demoteToHost flips the seeded player's role to Host so a subsequent
// PromoteAdmin is a real transition rather than a no-op against the
// first-registrant Admin promotion.
func demoteToHost(t *testing.T, dbURI, displayName string) {
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
	if _, err := conn.ExecContext(
		t.Context(), "UPDATE players SET role = ? WHERE display_name = ?", auth.RoleHost, displayName,
	); err != nil {
		t.Fatalf("demote update err = %v, want nil", err)
	}
}

func TestResetPassword_UnknownEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	// Apply migrations so the players table exists.
	seedPlayer(t, dbURI, "someone-else")

	stdin := strings.NewReader("new-correct-battery\nnew-correct-battery\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "ghost@example.test")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for unknown email")
	}
	if got, want := err, ErrResetUserNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	// The user-not-found check fires before any prompt, so stdout should
	// remain empty - no point asking for a password we will never use.
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty (unknown email should fail before prompting)", got)
	}
}

func TestResetPassword_PasswordTooShort_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	stdin := strings.NewReader("short\n")
	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice@example.test")
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
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice@example.test")
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
	err := ResetPassword(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice@example.test")
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
	err := ResetPassword(t.Context(), envFor(dbURI), strings.NewReader(""), &stdout, &stderr, "alice@example.test")
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
