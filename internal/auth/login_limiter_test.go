package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestLoginRateLimiter_AllowsFirstThenBlocks(t *testing.T) {
	t.Parallel()

	limiter := NewLoginRateLimiter(3*time.Second, nil)
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

func TestLoginRateLimiter_PerIP(t *testing.T) {
	t.Parallel()

	limiter := NewLoginRateLimiter(3*time.Second, nil)
	if _, ok := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("first IP allow = false, want true")
	}
	if _, ok := limiter.Allow("5.6.7.8"); !ok {
		t.Error("second IP allow = false, want true (limiter is per-IP)")
	}
}

// TestLoginRateLimiter_AdmitsAfterWindow pins that advancing past the
// window re-admits the same IP. Uses the injected clock to avoid a
// real sleep.
func TestLoginRateLimiter_AdmitsAfterWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	limiter := NewLoginRateLimiterWithClock(3*time.Second, clock, nil)

	if _, ok := limiter.Allow("1.2.3.4"); !ok {
		t.Fatal("first Allow = false, want true")
	}
	if _, ok := limiter.Allow("1.2.3.4"); ok {
		t.Fatal("second Allow within window = true, want false")
	}

	now = now.Add(4 * time.Second)
	if _, ok := limiter.Allow("1.2.3.4"); !ok {
		t.Error("Allow after window = false, want true")
	}
}

// TestHandleLoginSubmit_RateLimited pins the #494 contract: a second
// POST inside the cool-down renders the 429 banner regardless of
// credential validity, with Retry-After set. Uses the same fake
// httptest peer IP so both requests share the limiter bucket.
func TestHandleLoginSubmit_RateLimited(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	limiter := NewLoginRateLimiter(time.Minute, nil)
	handler := HandleLoginSubmit(
		discardLogger(), nil,
		loginDeps(players, session.New([]byte("k"), true), limiter),
	)

	first := postLogin(t, handler, "alice", "wrong-password")
	if got, want := first.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("first POST status = %d, want %d", got, want)
	}

	second := postLogin(t, handler, "alice", "wrong-password")
	if got, want := second.Code, http.StatusTooManyRequests; got != want {
		t.Errorf("second POST status = %d, want %d", got, want)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
	if got, want := second.Body.String(), "Too many attempts"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q, got %q", want, got)
	}
}

// TestHandleLoginSubmit_RateLimitedFiresOnUnknownUser pins the
// "always trip regardless of displayName existence" design constraint:
// the limiter check runs BEFORE the credential lookup, so two POSTs
// against a non-existent displayName also trip the second response.
func TestHandleLoginSubmit_RateLimitedFiresOnUnknownUser(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	limiter := NewLoginRateLimiter(time.Minute, nil)
	handler := HandleLoginSubmit(
		discardLogger(), nil,
		loginDeps(players, session.New([]byte("k"), true), limiter),
	)

	first := postLogin(t, handler, "ghost", "whatever-password")
	if got, want := first.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("first POST status = %d, want %d", got, want)
	}

	second := postLogin(t, handler, "ghost", "whatever-password")
	if got, want := second.Code, http.StatusTooManyRequests; got != want {
		t.Errorf("second POST status = %d, want %d (limiter must fire on unknown users too)", got, want)
	}
}

// TestHandleLoginSubmit_RateLimitedRefusesCorrectCredentials pins the
// invariant that the limiter check runs BEFORE the credential check,
// so a hot bucket blocks a valid login attempt too. Without this, an
// attacker who burned the window with garbage attempts could still
// race a legitimate user's correct submission through.
func TestHandleLoginSubmit_RateLimitedRefusesCorrectCredentials(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := players.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, players, "alice")

	limiter := NewLoginRateLimiter(time.Minute, nil)
	handler := HandleLoginSubmit(
		discardLogger(), nil,
		loginDeps(players, session.New([]byte("k"), true), limiter),
	)

	first := postLogin(t, handler, "alice", "wrong-password")
	if got, want := first.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("first POST status = %d, want %d", got, want)
	}

	second := postLogin(t, handler, "alice", "correctbattery")
	if got, want := second.Code, http.StatusTooManyRequests; got != want {
		t.Errorf(
			"second POST status = %d, want %d (limiter must gate even valid creds)",
			got, want,
		)
	}
}

func postLogin(t *testing.T, handler http.Handler, displayName, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"displayName": {displayName}, "password": {password}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}
