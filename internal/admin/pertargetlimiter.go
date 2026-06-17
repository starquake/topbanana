package admin

import (
	"sync"
	"time"
)

// PerTargetLimiter is a per-player-id cool-down for admin actions that
// dispatch outbound mail. Concurrency-safe; the map is pruned of stale
// entries every Allow call so memory stays proportional to the live
// caller set. Same shape as auth.VerifyResendLimiter but keyed on
// playerID instead of source IP.
type PerTargetLimiter struct {
	mu     sync.Mutex
	last   map[int64]time.Time
	window time.Duration
	now    func() time.Time
}

// NewPerTargetLimiter returns a limiter using the supplied window and
// [time.Now] as the clock. The clock is injectable via the export_test
// seam so tests can fast-forward without sleeping.
func NewPerTargetLimiter(window time.Duration) *PerTargetLimiter {
	return newPerTargetLimiterWithClock(window, time.Now)
}

func newPerTargetLimiterWithClock(window time.Duration, now func() time.Time) *PerTargetLimiter {
	return &PerTargetLimiter{
		last:   map[int64]time.Time{},
		window: window,
		now:    now,
	}
}

// Allow reports whether target may dispatch right now. On admit stamps
// the bucket so the next call within the window is blocked and returns
// the stamp as a token; pass it to [PerTargetLimiter.Cancel] to revert
// this specific stamp on a downstream bail-out. On block returns the
// remaining wait so the caller can render it, and the zero-time token.
func (l *PerTargetLimiter) Allow(target int64) (time.Duration, bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-2 * l.window)
	for k, ts := range l.last {
		if ts.Before(cutoff) {
			delete(l.last, k)
		}
	}
	if prev, ok := l.last[target]; ok {
		if remaining := l.window - now.Sub(prev); remaining > 0 {
			return remaining, false, time.Time{}
		}
	}
	l.last[target] = now

	return 0, true, now
}

// Cancel reverts the stamp Allow wrote for target, but only if the live
// stamp still matches token. Matching on token keeps a slow caller from
// clobbering a newer stamp a second concurrent caller wrote in between
// this caller's Allow and Cancel - that newer stamp must stand so the
// second caller's window is honoured. Idempotent: Cancel on a target
// with no entry, or with a stale token, is a no-op.
func (l *PerTargetLimiter) Cancel(target int64, token time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if cur, ok := l.last[target]; ok && cur.Equal(token) {
		delete(l.last, target)
	}
}
