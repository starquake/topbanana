package auth

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/request"
)

// POST /login is throttled on two independent axes:
//
//   - Per-IP gap ([LoginRateLimiter]): a short minimum gap between
//     consecutive attempts from one client address, checked BEFORE the
//     credential lookup. Blocks distributed spraying from a single host
//     and returns a 429; keyed on IP, so it reveals nothing about which
//     accounts exist (#494).
//   - Per-account backoff ([AccountLoginLimiter]): a failed-attempt
//     counter keyed on the submitted email. After accountLoginThreshold
//     consecutive failures the account enters a cooldown during which
//     even a correct password is refused with the ordinary
//     invalid-credentials response (#786). Slows a focused brute-force
//     against one account without an IP rotation defeating it.
//
// The per-account limiter is deliberately enumeration-opaque: a cooled-
// down account responds IDENTICALLY to a normal wrong-password attempt
// (same 401, same generic "invalid email or password" banner, same
// dummy-hash timing), so a probe cannot tell a hammered/known account
// apart from a never-tried one. It changes only WHETHER a correct
// password is accepted, never the response shape.

// loginCooldown is the per-IP minimum gap between consecutive
// POST /login attempts. A few seconds is short enough that a user
// fixing a typo barely notices, long enough that a distributed
// brute-force still spends real wall-clock time per guess on top of
// the bcrypt compare cost (#494).
const loginCooldown = 3 * time.Second

// accountLoginThreshold is the number of consecutive failed attempts
// for one account before the per-account cooldown engages. A handful of
// fat-fingered passwords stays under it; a brute-force trips it fast.
const accountLoginThreshold = 5

// accountLoginCooldown is how long an account stays in backoff after the
// threshold is reached. Measured from the most recent failure, so a
// brute-force that keeps guessing keeps the account locked while a real
// user who walks away is admitted again once it elapses.
const accountLoginCooldown = 15 * time.Minute

// AccountLoginThreshold exposes the per-account failure threshold so the
// wiring layer (and tests) can build an [AccountLoginLimiter] with the
// same value the handler reasons about.
func AccountLoginThreshold() int { return accountLoginThreshold }

// AccountLoginCooldown exposes the per-account backoff window so the
// wiring layer (and tests) can build an [AccountLoginLimiter] with the
// same value the handler reasons about.
func AccountLoginCooldown() time.Duration { return accountLoginCooldown }

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

// accountFailures tracks one account's recent failed-login streak.
type accountFailures struct {
	count    int
	lastSeen time.Time
}

// AccountLoginLimiter is the per-account half of the login throttle
// (#786). It counts consecutive failed attempts keyed on the submitted
// email and reports a cooldown once the streak reaches the threshold.
// Unlike [LoginRateLimiter] it does NOT short-circuit the handler with a
// distinct status: the caller folds [AccountLoginLimiter.InCooldown]
// into the ordinary invalid-credentials path so a cooled-down account is
// indistinguishable from a wrong-password attempt (no "locked" signal,
// no enumeration oracle).
//
// Concurrency-safe; stale entries are pruned on every call so memory
// stays proportional to the live attacked-account set rather than the
// lifetime set.
type AccountLoginLimiter struct {
	mu        sync.Mutex
	entries   map[string]*accountFailures
	threshold int
	cooldown  time.Duration
	now       func() time.Time
}

// NewAccountLoginLimiter returns a limiter that trips after threshold
// consecutive failures for one account and holds the cooldown from the
// most recent failure. [time.Now] is the clock; the export_test seam
// injects a fake clock so tests fast-forward without sleeping.
func NewAccountLoginLimiter(threshold int, cooldown time.Duration) *AccountLoginLimiter {
	return newAccountLoginLimiterWithClock(threshold, cooldown, time.Now)
}

func newAccountLoginLimiterWithClock(
	threshold int, cooldown time.Duration, now func() time.Time,
) *AccountLoginLimiter {
	return &AccountLoginLimiter{
		entries:   map[string]*accountFailures{},
		threshold: threshold,
		cooldown:  cooldown,
		now:       now,
	}
}

// InCooldown reports whether account has reached the failure threshold
// and is still inside the cooldown window. An empty account string is
// never in cooldown (a blank submitted email cannot identify a row).
func (l *AccountLoginLimiter) InCooldown(account string) bool {
	if account == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(l.now())
	e, ok := l.entries[account]
	if !ok {
		return false
	}

	return e.count >= l.threshold
}

// RegisterFailure records one failed attempt for account, extending the
// cooldown window from the current time. A blank account is ignored.
func (l *AccountLoginLimiter) RegisterFailure(account string) {
	if account == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)
	e, ok := l.entries[account]
	if !ok {
		e = &accountFailures{}
		l.entries[account] = e
	}
	e.count++
	e.lastSeen = now
}

// RegisterSuccess clears account's failure streak after a genuine
// sign-in, so a user who eventually types the right password is not
// penalised by earlier typos. A blank account is ignored.
func (l *AccountLoginLimiter) RegisterSuccess(account string) {
	if account == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, account)
}

// pruneLocked drops entries whose last failure has aged past the
// cooldown window, so a one-off wrong password does not pin memory.
// Caller holds l.mu.
func (l *AccountLoginLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-l.cooldown)
	for k, e := range l.entries {
		if e.lastSeen.Before(cutoff) {
			delete(l.entries, k)
		}
	}
}
