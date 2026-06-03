package auth_test

import (
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
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
