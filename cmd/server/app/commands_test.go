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
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
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

func TestVerifyEmail_HappyPath_MarksVerified(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		displayName = "alice"
		email       = "alice@example.test"
	)
	seedPlayer(t, dbURI, displayName)

	if got := fetchSeededPlayer(t, dbURI).IsEmailVerified(); got {
		t.Fatalf("IsEmailVerified before VerifyEmail = %v, want false", got)
	}

	var stdout, stderr bytes.Buffer
	if err := VerifyEmail(t.Context(), envFor(dbURI), &stdout, &stderr, email); err != nil {
		t.Fatalf("VerifyEmail err = %v, want nil", err)
	}

	if got, want := fetchSeededPlayer(t, dbURI).IsEmailVerified(), true; got != want {
		t.Errorf("IsEmailVerified after VerifyEmail = %v, want %v", got, want)
	}
	if got, want := stdout.String(), "Verified"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want substring %q", got, want)
	}
}

func TestVerifyEmail_UnknownEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := VerifyEmail(t.Context(), envFor(dbURI), &stdout, &stderr, "nobody@example.test")
	if got, want := err, ErrVerifyEmailNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestVerifyEmail_BlankEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := VerifyEmail(t.Context(), envFor(dbURI), &stdout, &stderr, "   ")
	if got, want := err, ErrVerifyEmailRequired; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// fetchPlayerByEmail re-opens dbURI and returns the player row for email.
func fetchPlayerByEmail(t *testing.T, dbURI, email string) *auth.Player {
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
	p, err := players.GetPlayerByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}

	return p
}

func TestCreateAdmin_HappyPath_CreatesVerifiedAdmin(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		email    = "founder@example.test"
		password = "correct-horse-battery"
	)
	stdin := strings.NewReader(password + "\n" + password + "\n")
	var stdout, stderr bytes.Buffer
	if err := CreateAdmin(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, email); err != nil {
		t.Fatalf("CreateAdmin err = %v, want nil", err)
	}

	p := fetchPlayerByEmail(t, dbURI, email)
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
	if !p.IsAdmin() {
		t.Error("IsAdmin() = false, want true")
	}
	if !p.IsEmailVerified() {
		t.Error("IsEmailVerified() = false, want true")
	}
	if err := auth.CheckPassword(p.PasswordHash, password); err != nil {
		t.Errorf("CheckPassword(password) err = %v, want nil", err)
	}
	if got, want := p.DisplayName, "founder"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "Created admin"; !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want substring %q", got, want)
	}
}

func TestCreateAdmin_ExistingEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	seedPlayer(t, dbURI, "alice")

	stdin := strings.NewReader("correct-horse-battery\ncorrect-horse-battery\n")
	var stdout, stderr bytes.Buffer
	err := CreateAdmin(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "alice@example.test")
	if got, want := err, ErrCreateAdminEmailExists; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestCreateAdmin_BlankEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := CreateAdmin(t.Context(), envFor(dbURI), strings.NewReader(""), &stdout, &stderr, "   ")
	if got, want := err, ErrCreateAdminEmailRequired; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestCreateAdmin_MalformedEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	var stdout, stderr bytes.Buffer
	err := CreateAdmin(t.Context(), envFor(dbURI), strings.NewReader(""), &stdout, &stderr, "admin@localhost")
	if got, want := err, ErrCreateAdminInvalidEmail; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestCreateAdmin_PasswordTooShort_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	stdin := strings.NewReader("short\n")
	var stdout, stderr bytes.Buffer
	err := CreateAdmin(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, "founder@example.test")
	if got, want := err, ErrPasswordTooShort; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestCreateAdmin_DisplayNameCollision_FallsBackToPetname(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	// seedPlayer claims display name "founder"; the new admin's email local
	// part is also "founder", forcing the petname fallback.
	seedPlayer(t, dbURI, "founder")

	const (
		email    = "founder@other.test"
		password = "correct-horse-battery"
	)
	stdin := strings.NewReader(password + "\n" + password + "\n")
	var stdout, stderr bytes.Buffer
	if err := CreateAdmin(t.Context(), envFor(dbURI), stdin, &stdout, &stderr, email); err != nil {
		t.Fatalf("CreateAdmin err = %v, want nil", err)
	}

	p := fetchPlayerByEmail(t, dbURI, email)
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
	if !p.IsEmailVerified() {
		t.Error("IsEmailVerified() = false, want true")
	}
	if got := p.DisplayName; got == "founder" {
		t.Errorf("DisplayName = %q, want a petname fallback distinct from the taken name", got)
	}
}

// TestSeedDemo_DisabledMode_ReturnsError pins the guard: SeedDemo refuses to
// seed when demo mode is off, so it can never populate a non-demo DB. The guard
// runs before any DB or archive access, so neither is needed. APP_ENV=development
// lets config.Parse succeed without a SESSION_KEY; the flag is read through the
// getenv argument, so no process-environment mutation is needed.
func TestSeedDemo_DisabledMode_ReturnsError(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		if key == "APP_ENV" {
			return "development"
		}

		return ""
	}
	var stderr bytes.Buffer
	err := SeedDemo(t.Context(), getenv, &stderr)
	if got, want := err, ErrSeedDemoDisabled; !errors.Is(got, want) {
		t.Errorf("SeedDemo err = %v, want %v", got, want)
	}
}

// TestSeedDemo_ArchiveNotSet_ReturnsError pins that SeedDemo rejects a missing
// DEMO_SEED_ARCHIVE_DIR before opening the database. Both flags are read through
// the getenv argument, so no process-environment mutation is needed.
func TestSeedDemo_ArchiveNotSet_ReturnsError(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		switch key {
		case "APP_ENV":
			return "development"
		case "DEMO_MODE_ENABLED":
			return "true"
		}

		return ""
	}
	var stderr bytes.Buffer
	err := SeedDemo(t.Context(), getenv, &stderr)
	if got, want := err, ErrSeedDemoArchiveNotSet; !errors.Is(got, want) {
		t.Errorf("SeedDemo err = %v, want %v", got, want)
	}
}

// TestSeedDemo_EmptyArchiveDir_ReturnsError pins that SeedDemo rejects a
// DEMO_SEED_ARCHIVE_DIR that holds no .zip files, before opening the database,
// so a misconfigured mount fails fast rather than seeding nothing.
func TestSeedDemo_EmptyArchiveDir_ReturnsError(t *testing.T) {
	t.Parallel()

	emptyDir := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "APP_ENV":
			return "development"
		case "DEMO_MODE_ENABLED":
			return "true"
		case "DEMO_SEED_ARCHIVE_DIR":
			return emptyDir
		}

		return ""
	}
	var stderr bytes.Buffer
	err := SeedDemo(t.Context(), getenv, &stderr)
	if got, want := err, demo.ErrNoArchives; !errors.Is(got, want) {
		t.Errorf("SeedDemo err = %v, want %v", got, want)
	}
}

// openMigratedPlayerStore opens a fresh connection to the migrated DB at dbURI
// and returns a concrete PlayerStore, the type bootstrapInitialAdmin takes.
func openMigratedPlayerStore(t *testing.T, dbURI string) *store.PlayerStore {
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
	if err := database.Migrate(conn); err != nil {
		t.Fatalf("Migrate err = %v, want nil", err)
	}

	return store.NewPlayerStore(conn, slog.Default())
}

// countPlayers returns the total number of rows in the players table, used to
// assert bootstrapInitialAdmin did not insert when it should have skipped.
func countPlayers(t *testing.T, dbURI string) int {
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
	var n int
	if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM players").Scan(&n); err != nil {
		t.Fatalf("count players err = %v, want nil", err)
	}

	return n
}

// discardLogger is a slog logger that drops output, for bootstrap tests that
// assert on state and return values rather than log output.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestBootstrapInitialAdmin_NoAdmin_CreatesVerifiedAdmin(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	const (
		email    = "founder@example.test"
		password = "correct-horse-battery"
	)
	cfg := &config.Config{InitialAdminEmail: email, InitialAdminPassword: password}
	if err := BootstrapInitialAdmin(t.Context(), cfg, openMigratedPlayerStore(t, dbURI), discardLogger()); err != nil {
		t.Fatalf("BootstrapInitialAdmin err = %v, want nil", err)
	}

	p := fetchPlayerByEmail(t, dbURI, email)
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
	if !p.IsEmailVerified() {
		t.Error("IsEmailVerified() = false, want true")
	}
	if err := auth.CheckPassword(p.PasswordHash, password); err != nil {
		t.Errorf("CheckPassword(password) err = %v, want nil", err)
	}
}

func TestBootstrapInitialAdmin_AdminPresent_NoNewAdmin(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)
	// The first credentialled registrant becomes admin automatically, so the DB
	// now holds one admin; bootstrap must be a no-op against it.
	seedPlayer(t, dbURI, "alice")

	before := countPlayers(t, dbURI)
	cfg := &config.Config{InitialAdminEmail: "founder@example.test", InitialAdminPassword: "correct-horse-battery"}
	if err := BootstrapInitialAdmin(t.Context(), cfg, openMigratedPlayerStore(t, dbURI), discardLogger()); err != nil {
		t.Fatalf("BootstrapInitialAdmin err = %v, want nil", err)
	}

	if got, want := countPlayers(t, dbURI), before; got != want {
		t.Errorf("player count after bootstrap = %d, want %d (no new row)", got, want)
	}
}

func TestBootstrapInitialAdmin_EmailInUse_SkipsWithoutError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	// Insert a non-admin owning the target email while no admin exists, so the
	// bootstrap reaches the "email already in use" skip branch.
	const email = "founder@example.test"
	seedNonAdminWithEmail(t, dbURI, "founder", email)

	before := countPlayers(t, dbURI)
	cfg := &config.Config{InitialAdminEmail: email, InitialAdminPassword: "correct-horse-battery"}
	players := openMigratedPlayerStore(t, dbURI)
	if err := BootstrapInitialAdmin(t.Context(), cfg, players, discardLogger()); err != nil {
		t.Fatalf("BootstrapInitialAdmin err = %v, want nil", err)
	}

	if got, want := countPlayers(t, dbURI), before; got != want {
		t.Errorf("player count after bootstrap = %d, want %d (no new row)", got, want)
	}
	hasAdmin, err := players.HasAnyAdmin(t.Context())
	if err != nil {
		t.Fatalf("HasAnyAdmin err = %v, want nil", err)
	}
	if hasAdmin {
		t.Error("HasAnyAdmin() = true, want false (bootstrap should not have promoted)")
	}
}

func TestBootstrapInitialAdmin_PasswordTooShort_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	cfg := &config.Config{InitialAdminEmail: "founder@example.test", InitialAdminPassword: "short"}
	err := BootstrapInitialAdmin(t.Context(), cfg, openMigratedPlayerStore(t, dbURI), discardLogger())
	if got, want := err, ErrInitialAdminPasswordTooShort; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestBootstrapInitialAdmin_PasswordTooLong_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	tooLong := strings.Repeat("a", auth.MaxPasswordLength+1)
	cfg := &config.Config{InitialAdminEmail: "founder@example.test", InitialAdminPassword: tooLong}
	err := BootstrapInitialAdmin(t.Context(), cfg, openMigratedPlayerStore(t, dbURI), discardLogger())
	if got, want := err, ErrInitialAdminPasswordTooLong; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestBootstrapInitialAdmin_MalformedEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	cfg := &config.Config{InitialAdminEmail: "admin@localhost", InitialAdminPassword: "correct-horse-battery"}
	err := BootstrapInitialAdmin(t.Context(), cfg, openMigratedPlayerStore(t, dbURI), discardLogger())
	if got, want := err, ErrInitialAdminInvalidEmail; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestBootstrapInitialAdmin_EmptyEnv_NoOp(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	before := countPlayers(t, dbURI)
	players := openMigratedPlayerStore(t, dbURI)
	cfg := &config.Config{}
	if err := BootstrapInitialAdmin(t.Context(), cfg, players, discardLogger()); err != nil {
		t.Fatalf("BootstrapInitialAdmin err = %v, want nil", err)
	}

	if got, want := countPlayers(t, dbURI), before; got != want {
		t.Errorf("player count after empty-env bootstrap = %d, want %d (no new row)", got, want)
	}
}

// seedNonAdminWithEmail inserts a player row holding the given email at the
// player tier without any admin present, the state the "email already in use"
// skip branch needs. A normal registration would auto-promote the first
// credentialled row to admin, which would short-circuit before that branch.
func seedNonAdminWithEmail(t *testing.T, dbURI, displayName, email string) {
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
	if err := database.Migrate(conn); err != nil {
		t.Fatalf("Migrate err = %v, want nil", err)
	}
	if _, err := conn.ExecContext(
		t.Context(),
		"INSERT INTO players (display_name, email, role) VALUES (?, ?, ?)",
		displayName, email, auth.RolePlayer,
	); err != nil {
		t.Fatalf("insert non-admin err = %v, want nil", err)
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
	if got, want := err, ErrPasswordTooShort; !errors.Is(got, want) {
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
	if got, want := err, ErrPasswordTooLong; !errors.Is(got, want) {
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
	if got, want := err, ErrPasswordsDontMatch; !errors.Is(got, want) {
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
	if got, want := err, ErrEmptyInput; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}

	p := fetchSeededPlayer(t, dbURI)
	if err := auth.CheckPassword(p.PasswordHash, seedOldPassword); err != nil {
		t.Errorf("original password should still validate after empty stdin, err = %v", err)
	}
}
