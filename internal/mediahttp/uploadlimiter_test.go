package mediahttp_test

import (
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/mediahttp"
)

// mutableClock is a test clock whose now value the test advances by hand, so
// the limiter's window math is exercised without sleeping.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestUploadBudgetLimiter_Charge(t *testing.T) {
	t.Parallel()

	const window = time.Minute
	const budget = 5
	const player int64 = 42

	t.Run("a single charge within budget admits", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		allowed, retryAfter := limiter.Charge(player, 3)
		if !allowed {
			t.Error("Charge = false, want true within budget")
		}
		if got, want := retryAfter, time.Duration(0); got != want {
			t.Errorf("retryAfter = %v, want %v on an admit", got, want)
		}
	})

	t.Run("charges summing past budget within the window block", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		if allowed, _ := limiter.Charge(player, 4); !allowed {
			t.Fatal("first Charge = false, want true")
		}
		allowed, retryAfter := limiter.Charge(player, 2)
		if allowed {
			t.Error("Charge = true, want false once the running total exceeds budget")
		}
		if retryAfter <= 0 {
			t.Errorf("retryAfter = %v, want a positive remaining duration", retryAfter)
		}
	})

	t.Run("a blocked charge does not draw down the budget", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		if allowed, _ := limiter.Charge(player, 4); !allowed {
			t.Fatal("first Charge = false, want true")
		}
		if allowed, _ := limiter.Charge(player, 3); allowed {
			t.Fatal("over-budget Charge = true, want false")
		}
		// The rejected charge of 3 must not have been recorded, so a charge of
		// 1 (4+1 <= 5) still fits.
		if allowed, _ := limiter.Charge(player, 1); !allowed {
			t.Error("Charge(1) = false, want true (the blocked charge was not recorded)")
		}
	})

	t.Run("a request whose own n exceeds budget is rejected with the full window", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		allowed, retryAfter := limiter.Charge(player, budget+1)
		if allowed {
			t.Error("Charge = true, want false when n alone exceeds budget")
		}
		if got, want := retryAfter, window; got != want {
			t.Errorf("retryAfter = %v, want the full window %v", got, want)
		}
	})

	t.Run("after the window elapses the budget frees", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		if allowed, _ := limiter.Charge(player, budget); !allowed {
			t.Fatal("first Charge = false, want true")
		}
		if allowed, _ := limiter.Charge(player, 1); allowed {
			t.Fatal("Charge at full budget = true, want false")
		}

		clock.Advance(window)
		if allowed, retryAfter := limiter.Charge(player, budget); !allowed {
			t.Errorf("Charge = false, want true once the window has elapsed (retryAfter=%v)", retryAfter)
		}
	})

	t.Run("charges from different players are independent", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		if allowed, _ := limiter.Charge(player, budget); !allowed {
			t.Fatal("first player Charge = false, want true")
		}
		// A second player has a fresh budget despite the first being exhausted.
		if allowed, _ := limiter.Charge(player+1, budget); !allowed {
			t.Error("second player Charge = false, want true (per-player budgets are independent)")
		}
	})

	t.Run("a non-positive budget disables the limiter", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(0, window, clock.Now)

		// Budget zero is the documented "disabled" setting: every charge admits,
		// no matter how large, and nothing is tracked.
		allowed, retryAfter := limiter.Charge(player, budget+100)
		if !allowed {
			t.Error("Charge = false, want true when budget is zero (limiter disabled)")
		}
		if got, want := retryAfter, time.Duration(0); got != want {
			t.Errorf("retryAfter = %v, want %v on a disabled limiter", got, want)
		}
		if got, want := UploadBudgetLimiterEntryCount(limiter), 0; got != want {
			t.Errorf("entry count = %d, want %d (a disabled limiter records nothing)", got, want)
		}
	})

	t.Run("a non-positive window disables the limiter", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, 0, clock.Now)

		if allowed, _ := limiter.Charge(player, budget+100); !allowed {
			t.Error("Charge = false, want true when window is zero (limiter disabled)")
		}
		if got, want := UploadBudgetLimiterEntryCount(limiter), 0; got != want {
			t.Errorf("entry count = %d, want %d (a disabled limiter records nothing)", got, want)
		}
	})

	t.Run("stale entries are pruned once their charges age out", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewUploadBudgetLimiterWithClock(budget, window, clock.Now)

		if allowed, _ := limiter.Charge(player, 1); !allowed {
			t.Fatal("first Charge = false, want true")
		}
		if got, want := UploadBudgetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d after one charge", got, want)
		}

		// Advance past the window and charge a different player so the prune
		// sweep runs and drops the now-stale first player.
		clock.Advance(window + time.Second)
		if allowed, _ := limiter.Charge(player+1, 1); !allowed {
			t.Fatal("Charge(other) = false, want true")
		}
		if got, want := UploadBudgetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d (stale player pruned, only the new one remains)", got, want)
		}
	})
}
