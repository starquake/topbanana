package version_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/version"
)

// These tests mutate the package-level build-stamp vars and the
// process-wide env, so they must run serially: t.Parallel would let two
// cases clobber each other's Version/Commit/Env. The paralleltest linter
// is suppressed per test for that reason.

// setStamp installs the build-stamp vars for one test and restores them
// on cleanup, so cases that inject a Version/Commit do not leak into the
// fallback cases (go test runs without ldflags, so the vars start empty).
func setStamp(t *testing.T, ver, commit string) {
	t.Helper()

	prevVer, prevCommit := Version, Commit
	Version, Commit = ver, commit
	t.Cleanup(func() {
		Version, Commit = prevVer, prevCommit
	})
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_Production(t *testing.T) {
	setStamp(t, "2026.6.0", "abc1234def5678")
	SetEnv("production")

	if got, want := Label(), "v2026.6.0 (abc1234)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_Staging(t *testing.T) {
	setStamp(t, "2026.6.0", "abc1234def5678")
	SetEnv("staging")

	if got, want := Label(), "staging (abc1234)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_DevelopmentIgnoresVersion(t *testing.T) {
	setStamp(t, "2026.6.0", "abc1234def5678")
	SetEnv("development")

	if got, want := Label(), "development (abc1234)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_UnsetEnvFallsBackToDev(t *testing.T) {
	setStamp(t, "", "abc1234def5678")
	SetEnv("")

	if got, want := Label(), "dev (abc1234)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_ProductionWithoutVersionFallsBack(t *testing.T) {
	setStamp(t, "", "abc1234def5678")
	SetEnv("production")

	// Empty Version under a production env still renders without panicking;
	// Release falls back to "dev".
	if got, want := Label(), "vdev (abc1234)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestLabel_NonEmptyWithStampedCommit(t *testing.T) {
	setStamp(t, "2026.6.0", "")
	SetEnv("production")

	// go test provides a vcs.revision via ReadBuildInfo, so the commit is
	// non-empty here; assert the structural shape rather than a literal sha.
	if got := Label(); got == "" {
		t.Errorf("Label() = %q, want non-empty", got)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestCommitLabel_ShortensStampedCommit(t *testing.T) {
	setStamp(t, "", "0123456789abcdef")

	if got, want := CommitLabel(), "0123456"; got != want {
		t.Errorf("CommitLabel() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates shared package globals; must run serially.
func TestRelease_Fallback(t *testing.T) {
	setStamp(t, "", "")

	if got, want := Release(), "dev"; got != want {
		t.Errorf("Release() = %q, want %q", got, want)
	}
}
