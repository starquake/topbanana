package auth

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/request"
)

// loginCooldown is the per-IP minimum gap between consecutive
// POST /login attempts. A few seconds is short enough that a user
// fixing a typo barely notices, long enough that a distributed
// brute-force still spends real wall-clock time per guess on top of
// the bcrypt compare cost (#494).
const loginCooldown = 3 * time.Second

// LoginCooldown exposes the per-IP cool-down so the wiring layer can
// build a [LoginRateLimiter] with the same window the handler logs
// against. Same pattern as [VerifyResendCooldown].
func LoginCooldown() time.Duration { return loginCooldown }

// LoginRateLimiter is a per-IP cool-down for POST /login. The handler
// calls [LoginRateLimiter.Allow] before any credential lookup so the
// limiter fires whether or not the submitted displayName exists,
// preserving the same response-timing shape the dummy-hash compare
// already gives the credential-check path.
//
// Concurrency-safe; the map is pruned of stale entries every Allow
// call so memory stays proportional to the live caller set rather
// than the lifetime set.
//
// trustedProxyCIDRs is the allow-list of upstream proxies whose
// X-Forwarded-For header [LoginRateLimiter.ClientIP] honours when
// bucketing; nil means "trust nothing" so XFF is ignored and the
// bucket key is the request peer's address. See [request.ClientIP]
// for the walk semantics and #463 for the rationale.
type LoginRateLimiter struct {
	mu                sync.Mutex
	last              map[string]time.Time
	window            time.Duration
	now               func() time.Time
	trustedProxyCIDRs []*net.IPNet
}

// NewLoginRateLimiter returns a limiter using the supplied window,
// [time.Now] as the clock, and trustedProxyCIDRs as the per-IP bucket
// override list. nil/empty CIDR slice disables the XFF walk; see
// [LoginRateLimiter] for the policy. The clock is injectable via the
// export_test seam so tests can fast-forward without sleeping.
func NewLoginRateLimiter(window time.Duration, trustedProxyCIDRs []*net.IPNet) *LoginRateLimiter {
	return newLoginRateLimiterWithClock(window, time.Now, trustedProxyCIDRs)
}

func newLoginRateLimiterWithClock(
	window time.Duration, now func() time.Time, trustedProxyCIDRs []*net.IPNet,
) *LoginRateLimiter {
	return &LoginRateLimiter{
		last:              map[string]time.Time{},
		window:            window,
		now:               now,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}
}

// ClientIP resolves the per-IP bucket key from r using the
// trustedProxyCIDRs allow-list passed at construction. HTTP handlers
// pass the result to [LoginRateLimiter.Allow]; unit tests that pin
// Allow itself keep using Allow + a synthetic IP.
func (l *LoginRateLimiter) ClientIP(r *http.Request) string {
	return request.ClientIP(r, l.trustedProxyCIDRs)
}

// Allow reports whether ip may submit a login right now. On admit,
// stamps the bucket so the next call within the window is blocked. On
// block, returns the remaining wait so the caller can render it.
func (l *LoginRateLimiter) Allow(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-2 * l.window)
	for k, ts := range l.last {
		if ts.Before(cutoff) {
			delete(l.last, k)
		}
	}
	if prev, ok := l.last[ip]; ok {
		if remaining := l.window - now.Sub(prev); remaining > 0 {
			return remaining, false
		}
	}
	l.last[ip] = now

	return 0, true
}
