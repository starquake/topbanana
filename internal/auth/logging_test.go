package auth_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

// captureHandler is a slog.Handler that records every record at Debug+ so a
// test can assert the auth layer emitted the expected login-outcome line
// (#872) with the expected level and fields. Concurrency-safe so a handler
// that logs from a background goroutine cannot race the assertion.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]slog.Record
}

func newCaptureHandler() captureHandler {
	return captureHandler{mu: &sync.Mutex{}, records: &[]slog.Record{}}
}

func (captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, r.Clone())

	return nil
}

func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

func (h captureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]slog.Record(nil), *h.records...)
}

// assertLog asserts that a record with msg was captured at level want, and
// returns its attributes keyed by name so the caller can check fields.
func (h captureHandler) assertLog(t *testing.T, msg string, want slog.Level) map[string]slog.Value {
	t.Helper()

	var rec slog.Record
	found := false
	var msgs []string
	for _, r := range h.snapshot() {
		msgs = append(msgs, r.Message)
		if r.Message == msg {
			rec = r
			found = true

			break
		}
	}
	if !found {
		t.Fatalf("no log record with message %q (captured: %v)", msg, msgs)
	}
	if got := rec.Level; got != want {
		t.Errorf("log %q level = %v, want %v", msg, got, want)
	}

	attrs := make(map[string]slog.Value, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value

		return true
	})

	return attrs
}

func assertStringAttr(t *testing.T, attrs map[string]slog.Value, key, want string) {
	t.Helper()
	if got := attrs[key].String(); got != want {
		t.Errorf("attr %q = %q, want %q", key, got, want)
	}
}

func assertInt64Attr(t *testing.T, attrs map[string]slog.Value, key string, want int64) {
	t.Helper()
	if got := attrs[key].Int64(); got != want {
		t.Errorf("attr %q = %d, want %d", key, got, want)
	}
}

// seedPassword is the known password every seedVerifiedPlayer row carries;
// the login tests POST this to drive the correct-credentials paths.
const seedPassword = "correctbattery"

// seedVerifiedPlayer creates a verified player with the given role and the
// known [seedPassword], returning the new row's id.
func seedVerifiedPlayer(
	t *testing.T, players *store.PlayerStore, displayName, email, role string,
) int64 {
	t.Helper()
	hash, err := HashPassword(seedPassword)
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	p, err := players.CreatePlayer(t.Context(), displayName, email, hash, role)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, players, displayName)

	return p.ID
}

func TestHandleLoginSubmit_Logs_Success(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	id := seedVerifiedPlayer(t, players, "alice", "alice@example.test", RoleAdmin)

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {seedPassword},
	})

	attrs := logs.assertLog(t, "login succeeded", slog.LevelInfo)
	assertInt64Attr(t, attrs, "player", id)
	assertStringAttr(t, attrs, "email", "alice@example.test")
	assertStringAttr(t, attrs, "role", RoleAdmin)
}

func TestHandleLoginSubmit_Logs_UnknownAccount(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	postForm(t, handler, "/login", url.Values{
		"email":    {"ghost@example.test"},
		"password": {"whatever-password"},
	})

	attrs := logs.assertLog(t, "login failed: invalid credentials", slog.LevelInfo)
	assertStringAttr(t, attrs, "email", "ghost@example.test")
	assertStringAttr(t, attrs, "reason", "unknown-account")
}

func TestHandleLoginSubmit_Logs_NoPassword(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// A passwordless row (legacy seed admin): create anonymous then it has
	// no password hash. CreateAnonymousPlayer leaves email empty, so claim
	// the row with an email but a blank hash to model the no-password case.
	if _, err := players.CreatePlayer(t.Context(), "nopass", "nopass@example.test", "", RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, players, "nopass")

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	postForm(t, handler, "/login", url.Values{
		"email":    {"nopass@example.test"},
		"password": {"any-password"},
	})

	attrs := logs.assertLog(t, "login failed: invalid credentials", slog.LevelInfo)
	assertStringAttr(t, attrs, "email", "nopass@example.test")
	assertStringAttr(t, attrs, "reason", "no-password")
}

func TestHandleLoginSubmit_Logs_WrongPassword(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	seedVerifiedPlayer(t, players, "alice", "alice@example.test", RolePlayer)

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {"wrong-password"},
	})

	attrs := logs.assertLog(t, "login failed: invalid credentials", slog.LevelInfo)
	assertStringAttr(t, attrs, "email", "alice@example.test")
	assertStringAttr(t, attrs, "reason", "wrong-password")
}

func TestHandleLoginSubmit_Logs_RateLimited(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))

	// First POST primes the bucket; the second trips the per-IP limiter.
	postForm(t, handler, "/login", url.Values{"email": {"a@example.test"}, "password": {"x"}})
	postForm(t, handler, "/login", url.Values{"email": {"a@example.test"}, "password": {"x"}})

	attrs := logs.assertLog(t, "login blocked: rate limited", slog.LevelWarn)
	// httptest requests default to RemoteAddr 192.0.2.1:1234.
	assertStringAttr(t, attrs, "ip", "192.0.2.1")
	if got := attrs["wait"].Duration(); got <= 0 {
		t.Errorf("attr %q = %v, want > 0", "wait", got)
	}
}

func TestHandleLoginSubmit_Logs_AccountCooldown(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	seedVerifiedPlayer(t, players, "alice", "alice@example.test", RolePlayer)

	logs := newCaptureHandler()
	accountLimiter := NewAccountLoginLimiter(3, time.Minute)
	handler := HandleLoginSubmit(slog.New(logs), nil, LoginDeps{
		Players:        players,
		Sessions:       session.New([]byte("k"), true),
		Limiter:        NewLoginRateLimiter(0, nil),
		AccountLimiter: accountLimiter,
	})

	// Trip the per-account cooldown, then a further attempt is blocked by it.
	for range 3 {
		postForm(t, handler, "/login", url.Values{
			"email": {"alice@example.test"}, "password": {"wrong-password"},
		})
	}
	postForm(t, handler, "/login", url.Values{
		"email": {"alice@example.test"}, "password": {seedPassword},
	})

	attrs := logs.assertLog(t, "login blocked: account in cooldown", slog.LevelWarn)
	assertStringAttr(t, attrs, "email", "alice@example.test")
}

func TestHandleLoginSubmit_Logs_EmailNotVerified(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	hash, err := HashPassword(seedPassword)
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	p, err := players.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	// Deliberately NOT verified.

	logs := newCaptureHandler()
	handler := HandleLoginSubmit(slog.New(logs), nil,
		loginDeps(players, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {seedPassword},
	})

	attrs := logs.assertLog(t, "login blocked: email not verified", slog.LevelInfo)
	assertInt64Attr(t, attrs, "player", p.ID)
	assertStringAttr(t, attrs, "email", "alice@example.test")
}

func TestFinalizeGoogleSignIn_Logs_Success(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	id := seedVerifiedPlayer(t, players, "gopher", "gopher@example.test", RolePlayer)
	player, err := players.GetPlayerByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}

	logs := newCaptureHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login/google/callback", nil)
	ExportFinalizeGoogleSignIn(rec, req, slog.New(logs), players, session.New([]byte("k"), true), player)

	attrs := logs.assertLog(t, "google sign-in succeeded", slog.LevelInfo)
	assertInt64Attr(t, attrs, "player", id)
	assertStringAttr(t, attrs, "email", "gopher@example.test")
}
