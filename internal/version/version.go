// Package version exposes the build stamp (release version, commit, and
// build date) and the per-environment display label shown in the admin
// footer and the /version endpoint.
//
// Version and Date are stamped at build time via -ldflags
// "-X github.com/starquake/topbanana/internal/version.Version=..." (and
// .Commit / .Date). The Docker build reads Version from the committed
// VERSION file and passes Commit + Date as build args, because .git is
// dockerignored. In a local checkout the flags are absent, so Commit
// falls back to [runtime/debug.ReadBuildInfo]'s vcs.revision (plus a
// -dirty marker), which the working tree provides for free.
//
// The app environment is set once at boot by [SetEnv]; [Label] gates its
// output on it: production shows the release version, every other
// environment shows the commit, because only production builds from an
// actual release commit.
package version

import (
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/starquake/topbanana/internal/config"
)

// shortCommitLen is how many hex characters of the commit sha to show.
const shortCommitLen = 7

// Version, Commit, and Date are the build stamp. They are populated by
// -ldflags at build time; in an un-stamped build (go test, go run, a
// plain go build) they stay empty and the accessors fall back to
// ReadBuildInfo (Commit) or sentinel strings (Label).
//
//nolint:gochecknoglobals // set once at link time via -ldflags, never mutated at runtime.
var (
	Version string
	Commit  string
	Date    string
)

// appEnv holds the environment set at boot. [atomic.Pointer] keeps the
// read cheap on the template-render hot path and lets [SetEnv] run once
// at boot without locking, mirroring internal/envtag.
//
//nolint:gochecknoglobals // intentional process-wide env; mutated only at boot.
var appEnv atomic.Pointer[string]

// SetEnv stores the application environment for the rest of the
// process's life. Called from cmd/server/app at boot with
// cfg.AppEnvironment.
func SetEnv(env string) {
	appEnv.Store(&env)
}

// Env returns the stored application environment, or the empty string
// when SetEnv has not run. Empty is treated as non-production by [Label].
func Env() string {
	if p := appEnv.Load(); p != nil {
		return *p
	}

	return ""
}

// resolvedCommit returns the short commit sha. It prefers the
// ldflags-stamped Commit (the only source available inside the
// dockerignored image build) and otherwise falls back to the VCS
// revision ReadBuildInfo exposes in a working checkout, appending
// "-dirty" when the tree had uncommitted changes. Returns "" when no
// source is available.
func resolvedCommit() string {
	if Commit != "" {
		return shorten(Commit)
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	return commitFromSettings(info.Settings)
}

// commitFromSettings derives the short commit from a build's VCS
// settings: the short vcs.revision, plus a "-dirty" marker when
// vcs.modified is set. Returns "" when no revision is recorded (e.g. a
// build by file path, which Go does not VCS-stamp). Split out from
// resolvedCommit so the revision/dirty branches are unit-testable with
// synthetic settings, without depending on how the test binary was built.
func commitFromSettings(settings []debug.BuildSetting) string {
	var revision, modified string
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		default:
		}
	}
	if revision == "" {
		return ""
	}

	short := shorten(revision)
	if modified == "true" {
		short += "-dirty"
	}

	return short
}

// shorten truncates a commit sha to shortCommitLen characters. It
// preserves a trailing "-dirty" marker (which the Makefile appends to
// the ldflags-stamped commit for local `go run` dev) so truncation does
// not swallow it; the ReadBuildInfo path passes a bare revision and
// appends its own marker after.
func shorten(commit string) string {
	suffix := ""
	if rest, found := strings.CutSuffix(commit, "-dirty"); found {
		commit, suffix = rest, "-dirty"
	}
	if len(commit) > shortCommitLen {
		commit = commit[:shortCommitLen]
	}

	return commit + suffix
}

// CommitLabel returns the resolved short commit, or "unknown" when no
// source provided one.
func CommitLabel() string {
	if c := resolvedCommit(); c != "" {
		return c
	}

	return "unknown"
}

// Release returns the stamped release version, or "dev" when the build
// was not stamped (go run / go test / un-flagged go build).
func Release() string {
	if Version != "" {
		return Version
	}

	return "dev"
}

// Label is the display string shown in the admin footer. Production
// (the only environment built from a release commit) shows the release
// version; every other environment - and an unset one - shows the
// environment name. Both forms carry the commit so it is always the
// disambiguator. Never returns the empty string.
//
// Examples: production -> "v2026.6.0 (abc1234)"; staging ->
// "staging (abc1234)"; unset local dev -> "dev (abc1234-dirty)".
func Label() string {
	commit := CommitLabel()
	if Env() == config.AppEnvironmentProduction {
		return "v" + Release() + " (" + commit + ")"
	}

	name := Env()
	if name == "" {
		name = "dev"
	}

	return name + " (" + commit + ")"
}
