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

		wait, ok, token := limiter.Allow(target)
		if !ok {
			t.Error("Allow = false, want true on the first call")
		}
		if got, want := wait, time.Duration(0); got != want {
			t.Errorf("wait = %v, want %v on an admit", got, want)
		}
		if got, want := token, clock.Now(); !got.Equal(want) {
			t.Errorf("token = %v, want %v (the stamp written on admit)", got, want)
		}
	})

	t.Run("second call within the window blocks", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		if _, ok, _ := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}

		clock.Advance(window / 2)
		wait, ok, token := limiter.Allow(target)
		if ok {
			t.Error("Allow = true, want false within the window")
		}
		if wait <= 0 {
			t.Errorf("wait = %v, want a positive remaining duration", wait)
		}
		if got, want := token, (time.Time{}); !got.Equal(want) {
			t.Errorf("token = %v, want the zero time on a block", got)
		}
	})

	t.Run("call after the window admits again", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		if _, ok, _ := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}

		clock.Advance(window)
		wait, ok, _ := limiter.Allow(target)
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

		if _, ok, _ := limiter.Allow(target); !ok {
			t.Fatal("first Allow = false, want true")
		}
		if got, want := PerTargetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d after one admit", got, want)
		}

		// Advance past 2*window and touch a different target so the sweep
		// runs and prunes the stale entry for the original target.
		clock.Advance(2*window + time.Second)
		if _, ok, _ := limiter.Allow(target + 1); !ok {
			t.Fatal("Allow(other) = false, want true")
		}
		if got, want := PerTargetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count = %d, want %d (stale target pruned, only the new one remains)", got, want)
		}
	})
}

func TestPerTargetLimiter_Cancel(t *testing.T) {
	t.Parallel()

	const window = time.Minute
	const target int64 = 42

	t.Run("cancel reverts the stamp so the next call admits", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		_, ok, token := limiter.Allow(target)
		if !ok {
			t.Fatal("first Allow = false, want true")
		}
		limiter.Cancel(target, token)
		if got, want := PerTargetLimiterEntryCount(limiter), 0; got != want {
			t.Errorf("entry count after Cancel = %d, want %d", got, want)
		}

		// The reverted stamp means an immediate retry within the window admits.
		if _, ok, _ := limiter.Allow(target); !ok {
			t.Error("Allow after Cancel = false, want true (window was reverted)")
		}
	})

	t.Run("cancel leaves a newer stamp from a concurrent caller alone", func(t *testing.T) {
		t.Parallel()

		clock := &mutableClock{now: time.Unix(0, 0)}
		limiter := NewPerTargetLimiterWithClock(window, clock.Now)

		_, ok, stale := limiter.Allow(target)
		if !ok {
			t.Fatal("first Allow = false, want true")
		}

		// A second caller re-stamps once the window has elapsed; the first
		// caller's stale Cancel must not clobber that newer stamp.
		clock.Advance(window)
		if _, ok, _ := limiter.Allow(target); !ok {
			t.Fatal("re-admit Allow = false, want true")
		}

		limiter.Cancel(target, stale)
		if got, want := PerTargetLimiterEntryCount(limiter), 1; got != want {
			t.Errorf("entry count after stale Cancel = %d, want %d (newer stamp kept)", got, want)
		}
	})
}
