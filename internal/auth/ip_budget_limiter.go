package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/request"
)

// IPBudgetLimiter admits up to budget requests per client IP within a
// trailing window. Unlike [LoginRateLimiter]'s single-gap cool-down it allows
// a burst, so many players behind one NAT (a classroom) can all get through at
// once while a loop from one address is still bounded. Concurrency-safe; stale
// entries are pruned on every Allow so memory stays proportional to the live
// caller set. A non-positive budget or window disables the limiter.
type IPBudgetLimiter struct {
	mu                sync.Mutex
	stamps            map[string][]time.Time
	budget            int
	window            time.Duration
	now               func() time.Time
	trustedProxyCIDRs []*net.IPNet
}

// NewIPBudgetLimiter returns a limiter admitting budget requests per IP per
// window, bucketing by [request.ClientIP] with trustedProxyCIDRs.
func NewIPBudgetLimiter(budget int, window time.Duration, trustedProxyCIDRs []*net.IPNet) *IPBudgetLimiter {
	return newIPBudgetLimiterWithClock(budget, window, time.Now, trustedProxyCIDRs)
}

func newIPBudgetLimiterWithClock(
	budget int, window time.Duration, now func() time.Time, trustedProxyCIDRs []*net.IPNet,
) *IPBudgetLimiter {
	return &IPBudgetLimiter{
		stamps:            map[string][]time.Time{},
		budget:            budget,
		window:            window,
		now:               now,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}
}

// ClientIP resolves the per-IP bucket key from r.
func (l *IPBudgetLimiter) ClientIP(r *http.Request) string {
	return request.ClientIP(r, l.trustedProxyCIDRs)
}

// Allow reports whether ip may make one more request now, recording it on
// admit. On block it returns how long until the oldest in-window request
// ages out.
func (l *IPBudgetLimiter) Allow(ip string) (time.Duration, bool) {
	if l.budget <= 0 || l.window <= 0 {
		return 0, true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)

	live := l.stamps[ip]
	if len(live) >= l.budget {
		return live[0].Add(l.window).Sub(now), false
	}
	l.stamps[ip] = append(live, now)

	return 0, true
}

// prune drops stamps at or past the trailing window and deletes an IP's
// entry once it has none left.
func (l *IPBudgetLimiter) prune(now time.Time) {
	cutoff := now.Add(-l.window)
	for ip, stamps := range l.stamps {
		kept := stamps[:0]
		for _, ts := range stamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(l.stamps, ip)

			continue
		}
		l.stamps[ip] = kept
	}
}

// LimitByIP answers 429 with a Retry-After header, without calling next, when
// the request's client IP is over limiter's budget.
func LimitByIP(next http.Handler, limiter *IPBudgetLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wait, ok := limiter.Allow(limiter.ClientIP(r)); !ok {
			writeTooManyRequests(w, wait)

			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeTooManyRequests writes a 429 whose Retry-After rounds wait up to whole
// seconds, so a sub-second remainder never reads as "retry now".
func writeTooManyRequests(w http.ResponseWriter, wait time.Duration) {
	seconds := max(int((wait+time.Second-1)/time.Second), 1)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}
