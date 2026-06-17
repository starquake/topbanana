package mediahttp

import (
	"sync"
	"time"
)

// UploadBudgetLimiter caps how many image files one host may upload within a
// rolling window, keyed by player id. It charges the file COUNT per request
// (not one tick per request) so the one-XHR-per-file upload pattern cannot
// bypass the per-request count cap: 500 single-file POSTs draw down the same
// budget a single 500-file POST would (#988). Concurrency-safe; the map is
// pruned of stale callers on every Charge so memory stays proportional to the
// live caller set (same prune-on-each-call shape as admin.PerTargetLimiter).
type UploadBudgetLimiter struct {
	mu      sync.Mutex
	charges map[int64][]time.Time
	budget  int
	window  time.Duration
	now     func() time.Time
}

// NewUploadBudgetLimiter returns a limiter allowing up to budget files per
// rolling window per player, using [time.Now] as the clock. The clock is
// injectable via the export_test seam so tests can fast-forward without
// sleeping.
func NewUploadBudgetLimiter(budget int, window time.Duration) *UploadBudgetLimiter {
	return newUploadBudgetLimiterWithClock(budget, window, time.Now)
}

func newUploadBudgetLimiterWithClock(budget int, window time.Duration, now func() time.Time) *UploadBudgetLimiter {
	return &UploadBudgetLimiter{
		charges: map[int64][]time.Time{},
		budget:  budget,
		window:  window,
		now:     now,
	}
}

// Charge records an upload of n files against playerID's budget. It returns
// (true, 0) when the n files fit within the trailing window and records the
// charge; otherwise it returns (false, retryAfter) WITHOUT recording, where
// retryAfter is how long until enough in-window charges age out to fit n.
//
// A non-positive budget or window disables the limiter: every charge admits and
// nothing is recorded. This mirrors the per-quiz cap's "zero disables" contract
// and avoids the degenerate window<=0 case where prune would drop every stamp on
// the same tick, so the budget could never accumulate.
//
// The window slides: only charges newer than now-window count toward the
// budget, so the limit is "budget files per trailing window" rather than a
// fixed bucket that resets on a wall-clock boundary. A request whose own n
// already exceeds budget can never fit, so it is rejected with the full window
// as retryAfter.
func (l *UploadBudgetLimiter) Charge(playerID int64, n int) (allowed bool, retryAfter time.Duration) {
	if l.budget <= 0 || l.window <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)

	live := l.charges[playerID]
	if len(live)+n <= l.budget {
		stamps := make([]time.Time, n)
		for i := range stamps {
			stamps[i] = now
		}
		l.charges[playerID] = append(live, stamps...)

		return true, 0
	}

	return false, l.retryAfterFor(live, now, n)
}

// retryAfterFor returns how long the caller should wait before n more files
// fit under budget. prune has already dropped expired stamps, so live holds
// only in-window charges sorted oldest-first (Charge only ever appends "now").
// It finds the oldest charges that must age out to leave room for n and
// returns the time until the last of those ages past the window. A request
// whose n alone exceeds budget can never fit, so it falls back to the window.
func (l *UploadBudgetLimiter) retryAfterFor(live []time.Time, now time.Time, n int) time.Duration {
	if n > l.budget {
		return l.window
	}
	// Reaching here means this is the reject path with n <= budget, so
	// len(live)+n > budget and the recorded-charge invariant len(live) <= budget
	// both hold; toFree is therefore in [1, len(live)].
	toFree := len(live) + n - l.budget
	// The (toFree-1)th oldest stamp is the last one that must expire; it ages
	// out of the window at stamp+window.
	expiresAt := live[toFree-1].Add(l.window)
	wait := expiresAt.Sub(now)
	if wait <= 0 {
		// A boundary rounding case; never report a non-positive wait.
		return time.Nanosecond
	}

	return wait
}

// prune drops every charge at or past the trailing window, removing a player's
// entry entirely once it has no live charges so the map stays proportional to
// the live caller set. A charge exactly window old counts as expired (frees
// budget) so a caller can resume after waiting precisely the window.
func (l *UploadBudgetLimiter) prune(now time.Time) {
	cutoff := now.Add(-l.window)
	for id, stamps := range l.charges {
		kept := stamps[:0]
		for _, ts := range stamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(l.charges, id)

			continue
		}
		l.charges[id] = kept
	}
}
