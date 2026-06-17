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

// TestHandleLoginSubmit_AccountCooldown_RejectsCorrectPassword pins the
// #786 contract end to end: after the threshold of failures for one
// account, even the correct password is refused, and the refusal is the
// SAME generic 401 invalid-credentials response a wrong password gets -
// no "locked" signal, no status change. The per-IP limiter is given a
// zero window so it never fires and the per-account path is what gates.
func TestHandleLoginSubmit_AccountCooldown_RejectsCorrectPassword(t *testing.T) {
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

	accountLimiter := NewAccountLoginLimiter(3, time.Minute)
	handler := HandleLoginSubmit(discardLogger(), nil, LoginDeps{
		Players:        players,
		Sessions:       session.New([]byte("k"), true),
		Limiter:        NewLoginRateLimiter(0, nil),
		AccountLimiter: accountLimiter,
	})

	// Three wrong-password attempts trip the per-account cooldown; each
	// is the ordinary 401.
	for i := range 3 {
		rec := postLoginEmail(t, handler, "alice@example.test", "wrong-password")
		if got, want := rec.Code, http.StatusUnauthorized; got != want {
			t.Fatalf("attempt %d status = %d, want %d", i+1, got, want)
		}
	}

	// The correct password now arrives, but the account is cooled down:
	// refused with the identical generic 401, no session cookie.
	rec := postLoginEmail(t, handler, "alice@example.test", "correctbattery")
	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Fatalf("correct-password-during-cooldown status = %d, want %d (must read as wrong password)", got, want)
	}
	if got, want := rec.Body.String(), "Invalid email or password."; !strings.Contains(got, want) {
		t.Errorf("body should be the generic invalid-credentials banner; got %.300q", got)
	}
	if dontWant := "locked"; strings.Contains(strings.ToLower(rec.Body.String()), dontWant) {
		t.Errorf("body leaks a lock signal %q; got %.300q", dontWant, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("session cookie set during account cooldown: %+v", c)
		}
	}
}

// TestHandleLoginSubmit_AccountCooldown_OtherAccountUnaffected pins that
// the per-account cooldown is scoped to the hammered account: a
// different account with correct credentials still signs in while the
// first is locked.
func TestHandleLoginSubmit_AccountCooldown_OtherAccountUnaffected(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	for _, name := range []string{"alice", "bob"} {
		if _, err := players.CreatePlayer(t.Context(), name, name+"@example.test", hash, RolePlayer); err != nil {
			t.Fatalf("CreatePlayer %q err = %v, want nil", name, err)
		}
		markVerified(t, players, name)
	}

	accountLimiter := NewAccountLoginLimiter(3, time.Minute)
	handler := HandleLoginSubmit(discardLogger(), nil, LoginDeps{
		Players:        players,
		Sessions:       session.New([]byte("k"), true),
		Limiter:        NewLoginRateLimiter(0, nil),
		AccountLimiter: accountLimiter,
	})

	for range 3 {
		postLoginEmail(t, handler, "alice@example.test", "wrong-password")
	}
	// Alice is now cooled down; bob's correct credentials still admit.
	rec := postLoginEmail(t, handler, "bob@example.test", "correctbattery")
	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("bob login status = %d, want %d (other account must be unaffected)", got, want)
	}
}

// postLoginEmail POSTs /login with the email field the handler actually
// reads (distinct from postLogin, which posts a displayName field for
// the wrong-credentials rate-limit cases).
func postLoginEmail(t *testing.T, handler http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {email}, "password": {password}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestAccountLoginLimiter_TripsAfterThreshold pins #786: the limiter
// reports no cooldown until the failure count reaches the threshold,
// then reports a cooldown.
func TestAccountLoginLimiter_TripsAfterThreshold(t *testing.T) {
	t.Parallel()

	limiter := NewAccountLoginLimiter(3, time.Minute)
	if limiter.InCooldown("alice@example.test") {
		t.Fatal("InCooldown before any failure = true, want false")
	}
	for i := range 2 {
		limiter.RegisterFailure("alice@example.test")
		if limiter.InCooldown("alice@example.test") {
			t.Fatalf("InCooldown after %d failures = true, want false (threshold 3)", i+1)
		}
	}
	limiter.RegisterFailure("alice@example.test")
	if !limiter.InCooldown("alice@example.test") {
		t.Error("InCooldown after reaching threshold = false, want true")
	}
}

// TestAccountLoginLimiter_PerAccount pins that the failure streak is
// keyed on the account: hammering one account never cools down another.
func TestAccountLoginLimiter_PerAccount(t *testing.T) {
	t.Parallel()

	limiter := NewAccountLoginLimiter(3, time.Minute)
	for range 3 {
		limiter.RegisterFailure("alice@example.test")
	}
	if !limiter.InCooldown("alice@example.test") {
		t.Fatal("alice InCooldown = false, want true")
	}
	if limiter.InCooldown("bob@example.test") {
		t.Error("bob InCooldown = true, want false (limiter is per-account)")
	}
}

// TestAccountLoginLimiter_SuccessClears pins that a genuine sign-in
// resets the streak so earlier typos do not penalise the user.
func TestAccountLoginLimiter_SuccessClears(t *testing.T) {
	t.Parallel()

	limiter := NewAccountLoginLimiter(3, time.Minute)
	limiter.RegisterFailure("alice@example.test")
	limiter.RegisterFailure("alice@example.test")
	limiter.RegisterSuccess("alice@example.test")
	limiter.RegisterFailure("alice@example.test")
	if limiter.InCooldown("alice@example.test") {
		t.Error("InCooldown after success reset + 1 failure = true, want false")
	}
}

// TestAccountLoginLimiter_PrunesAfterCooldown pins that an aged-out
// streak is forgotten: once the cooldown window elapses past the last
// failure, the account is no longer in cooldown and its entry prunes.
// Uses the injected clock so no real time passes.
func TestAccountLoginLimiter_PrunesAfterCooldown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	limiter := NewAccountLoginLimiterWithClock(3, 15*time.Minute, clock)

	for range 3 {
		limiter.RegisterFailure("alice@example.test")
	}
	if !limiter.InCooldown("alice@example.test") {
		t.Fatal("InCooldown right after threshold = false, want true")
	}

	now = now.Add(16 * time.Minute)
	if limiter.InCooldown("alice@example.test") {
		t.Error("InCooldown after cooldown elapsed = true, want false (entry should prune)")
	}
}

// TestAccountLoginLimiter_BoundedLockout pins #995: a third party spraying
// failed logins cannot extend the lockout indefinitely. Once the streak
// reaches threshold+cap the window freezes, so further in-window failures
// no longer push the unlock time out; the account unlocks a bounded
// accountLoginCooldown after the freeze point, then a fresh streak must
// climb from scratch before it locks again. The freeze is observable only
// while the entry is alive, so the spray here lands inside the frozen
// window. Uses the injected clock so no real time passes.
func TestAccountLoginLimiter_BoundedLockout(t *testing.T) {
	t.Parallel()

	const (
		threshold = 3
		cooldown  = 15 * time.Minute
		account   = "victim@example.test"
		sprayGap  = 30 * time.Second
		// Enough post-cap failures to span most of the frozen window
		// without aging the (frozen) entry out of it.
		sprayBeyond = 20
	)
	freezePoint := threshold + AccountFailureExtensionCap

	tests := []struct {
		name         string
		waitFromCap  time.Duration
		wantCooldown bool
	}{
		{
			name:         "still locked just before the bounded window elapses",
			waitFromCap:  cooldown - time.Second,
			wantCooldown: true,
		},
		{
			name:         "unlocked once the bounded window elapses",
			waitFromCap:  cooldown + time.Second,
			wantCooldown: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return now }
			limiter := NewAccountLoginLimiterWithClock(threshold, cooldown, clock)

			// Drive the count to the cap so the window freezes at frozenAt.
			for range freezePoint {
				limiter.RegisterFailure(account)
			}
			frozenAt := now

			// Keep spraying inside the frozen window: these post-cap
			// failures must NOT push the unlock time past frozenAt+cooldown.
			for range sprayBeyond {
				now = now.Add(sprayGap)
				limiter.RegisterFailure(account)
			}

			now = frozenAt.Add(tc.waitFromCap)
			if got, want := limiter.InCooldown(account), tc.wantCooldown; got != want {
				t.Errorf("InCooldown at frozenAt+%v = %t, want %t", tc.waitFromCap, got, want)
			}
		})
	}
}

// TestAccountLoginLimiter_BlankAccountIgnored pins that a blank
// submitted email never trips a cooldown: an empty identifier cannot
// name a real row, so counting failures on "" would be meaningless.
func TestAccountLoginLimiter_BlankAccountIgnored(t *testing.T) {
	t.Parallel()

	limiter := NewAccountLoginLimiter(1, time.Minute)
	limiter.RegisterFailure("")
	if limiter.InCooldown("") {
		t.Error("InCooldown for blank account = true, want false")
	}
}
