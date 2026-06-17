package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/store"
)

// TestMain wires goose's global state up once for the package - calling
// SetupGoose from parallel tests races on the goose package-level fields.
func TestMain(m *testing.M) {
	database.SetupGoose()
	m.Run()
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

// TestResetPassword_EmptyEmail_ReturnsError covers the up-front guard:
// an empty (or whitespace-only) email should fail before any config
// parse or DB open, so the test passes a getenv that would itself error
// to confirm the guard fires first.
func TestResetPassword_EmptyEmail_ReturnsError(t *testing.T) {
	t.Parallel()

	// Intentionally bogus env: if the empty-email guard didn't fire
	// first, config.Parse would hit this and we'd see a different error.
	getenv := func(string) string { return "" }

	var stdout, stderr bytes.Buffer
	err := ResetPassword(t.Context(), getenv, strings.NewReader(""), &stdout, &stderr, "   ")
	if err == nil {
		t.Fatal("ResetPassword err = nil, want non-nil for whitespace-only email")
	}
	if got, want := err, ErrResetEmailRequired; !errors.Is(got, want) {
		t.Errorf("err = %v, want errors.Is(%v)", got, want)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty (guard should fire before any prompt)", got)
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

// stubInviteSweep mirrors stubVerifySweep for the invite side.
type stubInviteSweep struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubInviteSweep) DeleteExpiredInvites(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return s.err
}

func (s *stubInviteSweep) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// stubRetentionSweep counts how many times each retention method was
// called and optionally returns an error. Concurrent-safe so the sweep
// goroutine and the test can touch it from different goroutines.
type stubRetentionSweep struct {
	mu               sync.Mutex
	anonCalls        int
	gameCalls        int
	lastAnonDays     int
	lastGameDays     int
	anonErr, gameErr error
}

func (s *stubRetentionSweep) SweepStaleAnonymousPlayers(_ context.Context, days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.anonCalls++
	s.lastAnonDays = days

	return s.anonErr
}

func (s *stubRetentionSweep) SweepAbandonedGames(_ context.Context, days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gameCalls++
	s.lastGameDays = days

	return s.gameErr
}

func (s *stubRetentionSweep) AnonCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.anonCalls
}

func (s *stubRetentionSweep) GameCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.gameCalls
}

func (s *stubRetentionSweep) LastAnonDays() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.lastAnonDays
}

func (s *stubRetentionSweep) LastGameDays() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.lastGameDays
}

// stubMediaSweep counts how many times the media sweep ran. Concurrent-safe
// so the sweep goroutine and the test can touch it from different goroutines.
type stubMediaSweep struct {
	mu    sync.Mutex
	calls int
}

func (s *stubMediaSweep) SweepStaleNotReady(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return 0, nil
}

func (s *stubMediaSweep) Calls() int {
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
	invites := &stubInviteSweep{}
	retention := &stubRetentionSweep{}
	mediaSweep := &stubMediaSweep{}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		RunTokenSweep(
			ctx, slog.New(slog.DiscardHandler),
			verify, reset, invites, retention, mediaSweep, time.Millisecond,
		)
		close(done)
	}()

	// Wait until at least one tick lands on each store before cancelling.
	deadline := time.After(time.Second)
	for verify.Calls() <= 0 || reset.Calls() <= 0 || invites.Calls() <= 0 ||
		retention.AnonCalls() <= 0 || retention.GameCalls() <= 0 || mediaSweep.Calls() <= 0 {
		select {
		case <-deadline:
			t.Fatalf("sweep did not tick; verify=%d reset=%d invites=%d anon=%d game=%d media=%d",
				verify.Calls(), reset.Calls(), invites.Calls(),
				retention.AnonCalls(), retention.GameCalls(), mediaSweep.Calls())
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
	invites := &stubInviteSweep{err: errors.New("invite sweep failed")}
	retention := &stubRetentionSweep{
		anonErr: errors.New("anon sweep failed"),
		gameErr: errors.New("game sweep failed"),
	}
	mediaSweep := &stubMediaSweep{}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		RunTokenSweep(
			ctx, slog.New(slog.DiscardHandler),
			verify, reset, invites, retention, mediaSweep, time.Millisecond,
		)
		close(done)
	}()

	// Wait for at least two ticks per store so the "continue past error"
	// invariant is observable.
	deadline := time.After(time.Second)
	for verify.Calls() < 2 || reset.Calls() < 2 || invites.Calls() < 2 ||
		retention.AnonCalls() < 2 || retention.GameCalls() < 2 {
		select {
		case <-deadline:
			t.Fatalf("sweep did not tick twice; verify=%d reset=%d invites=%d anon=%d game=%d",
				verify.Calls(), reset.Calls(), invites.Calls(),
				retention.AnonCalls(), retention.GameCalls())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

// TestRunRetentionSweep_PassesConfiguredWindows pins that the helper wires the
// production retention windows (the store package constants) into each sweep,
// so the day counts have a single source of truth in Go rather than drifting
// between the scheduler and the SQL.
func TestRunRetentionSweep_PassesConfiguredWindows(t *testing.T) {
	t.Parallel()

	retention := &stubRetentionSweep{}

	RunRetentionSweep(t.Context(), slog.New(slog.DiscardHandler), retention)

	if got, want := retention.LastAnonDays(), store.AnonymousRetentionDays; got != want {
		t.Errorf("anon sweep days = %d, want %d", got, want)
	}
	if got, want := retention.LastGameDays(), store.AbandonedGameDays; got != want {
		t.Errorf("game sweep days = %d, want %d", got, want)
	}
}

// TestRunRetentionSweep_RunsGameSweepAfterAnonError pins that a failure in
// the anonymous-player sweep does not skip the abandoned-game sweep: both
// run on every pass regardless of the other's outcome.
func TestRunRetentionSweep_RunsGameSweepAfterAnonError(t *testing.T) {
	t.Parallel()

	retention := &stubRetentionSweep{anonErr: errors.New("anon sweep failed")}

	RunRetentionSweep(t.Context(), slog.New(slog.DiscardHandler), retention)

	if got, want := retention.AnonCalls(), 1; got != want {
		t.Errorf("anon sweep calls = %d, want %d", got, want)
	}
	if got, want := retention.GameCalls(), 1; got != want {
		t.Errorf("game sweep calls = %d, want %d", got, want)
	}
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
