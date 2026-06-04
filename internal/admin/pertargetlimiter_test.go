package admin_test

import (
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
)

// mutableClock is a test clock whose now value the test advances by hand,
// so the limiter's window math is exercised without sleeping.
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

func TestPerTargetLimiter_Allow(t *testing.T) {
	t.Parallel()

	const window = time.Minute
	const target int64 = 42

	t.Run("first call on a target admits", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		wait, ok := limiter.Allow(target)
		if !ok {
			t.Error("Allow = false, want true on the first call")
		}
		if got, want := wait, time.Duration(0); got != want {
			t.Errorf("wait = %v, want %v on an admit", got, want)
		}
	})

	t.Run("second call within the window blocks", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		if _, ok := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}

		clock.Advance(window / 2)
		wait, ok := limiter.Allow(target)
		if ok {
			t.Error("Allow = true, want false within the window")
		}
		if wait <= 0 {
			t.Errorf("wait = %v, want a positive remaining duration", wait)
		}
	})

	t.Run("call after the window admits again", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		if _, ok := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}

		clock.Advance(window)
		wait, ok := limiter.Allow(target)
		if !ok {
			t.Error("Allow = false, want true once the window has elapsed")
		}
		if got, want := wait, time.Duration(0); got != want {
			t.Errorf("wait = %v, want %v on a re-admit", got, want)
		}
	})

	t.Run("stale entries are evicted past twice the window", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		if _, ok := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}
		if got, want := PerTargetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d after one admit", got, want)
		}

		// Advance past 2*window and touch a different target so the sweep
		// runs and prunes the stale entry for the original target.
		clock.Advance(2*window + time.Second)
		if _, ok := limiter.Allow(target + 1); !ok {
			t.Fatal("Allow(other) = false, want true")
		}
		if got, want := PerTargetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d (stale target pruned, only the new one remains)", got, want)
		}
	})
}
