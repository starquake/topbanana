package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
)

func TestRequireVerifiedEmail_AnonymousPassesThrough(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })

	gate := RequireVerifiedEmail(next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
	gate.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called; anonymous request should pass through")
	}
}

func TestRequireVerifiedEmail_VerifiedPassesThrough(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })

	gate := RequireVerifiedEmail(next)
	rec := httptest.NewRecorder()
	verified := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	p := &Player{ID: 1, Email: "alice@example.test", EmailVerifiedAt: &verified}
	req := httptest.NewRequestWithContext(WithPlayer(t.Context(), p),
		http.MethodGet, "/admin", nil)
	gate.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called; verified player should pass through")
	}
}

func TestRequireVerifiedEmail_UnverifiedBouncesToPending(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler was called; unverified player must be bounced")
	})

	gate := RequireVerifiedEmail(next)
	rec := httptest.NewRecorder()
	p := &Player{ID: 1, Email: "alice@example.test", EmailVerifiedAt: nil}
	req := httptest.NewRequestWithContext(WithPlayer(t.Context(), p),
		http.MethodGet, "/admin", nil)
	gate.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/verify-email/pending"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestRequireVerifiedEmail_NoEmailPassesThrough(t *testing.T) {
	t.Parallel()

	// A player row with no email_address on file cannot verify, so the
	// gate must not loop them. The OAuth-stub case used to hit this
	// before #111 PR1 stamped email_verified_at at link time.
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })

	gate := RequireVerifiedEmail(next)
	rec := httptest.NewRecorder()
	p := &Player{ID: 1, Email: "", EmailVerifiedAt: nil}
	req := httptest.NewRequestWithContext(WithPlayer(t.Context(), p),
		http.MethodGet, "/admin", nil)
	gate.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called; no-email player should pass through (cannot verify)")
	}
}

func TestVerifyResendLimiter_AllowsFirstThenBlocks(t *testing.T) {
	t.Parallel()

	limiter := NewVerifyResendLimiter(time.Minute)
	wait, ok := limiter.Allow("1.2.3.4")
	if !ok {
		t.Fatalf("Allow first = (%v, %v), want (0, true)", wait, ok)
	}
	wait, ok = limiter.Allow("1.2.3.4")
	if ok {
		t.Errorf("Allow second = (%v, true), want blocked", wait)
	}
	if wait <= 0 {
		t.Errorf("Allow second wait = %v, want > 0", wait)
	}
}

func TestVerifyResendLimiter_PerIP(t *testing.T) {
	t.Parallel()

	limiter := NewVerifyResendLimiter(time.Minute)
	if _, ok := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("first IP allow = false, want true")
	}
	if _, ok := limiter.Allow("5.6.7.8"); !ok {
		t.Error("second IP allow = false, want true (limiter is per-IP)")
	}
}
