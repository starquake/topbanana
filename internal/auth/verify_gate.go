package auth

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/request"
	"github.com/starquake/topbanana/internal/session"
)

// verifyPendingPath is the interstitial route shown to authenticated
// players whose email_verified_at is still NULL. Constant so the
// middleware redirect, the gate exemption list, and the route
// registration share one string.
const verifyPendingPath = "/verify-email/pending"

// verifyResendCooldown is the per-IP gap between consecutive resend
// requests. Short enough that a user who didn't see the mail can try
// again within a minute, long enough that a stuck reload loop cannot
// dispatch hundreds of mails.
const verifyResendCooldown = 60 * time.Second

// VerifyResendCooldown exposes the per-IP cool-down so the wiring
// layer can build a [VerifyResendLimiter] with the same window the
// handler logs against.
func VerifyResendCooldown() time.Duration { return verifyResendCooldown }

// verifyPendingData backs the verify-email/pending template.
type verifyPendingData struct {
	Title             string
	Email             string
	Notice            string
	Error             string
	ResendDisabled    bool
	ResendWaitSeconds int
}

// RequireVerifiedEmail wraps the next handler so a signed-in player
// without a stamped email_verified_at is bounced to the interstitial
// instead of reaching the protected page. Anonymous-session visitors
// pass through unchanged - they have no password to verify against;
// the visitor's outer auth middleware (RequireAdmin, RequireAuthenticated)
// is what enforces the "must be signed in" precondition for whatever
// route this wraps.
//
// The middleware reads the player off [PlayerFromContext] so it must
// be mounted INSIDE RequireAdmin / RequireAuthenticated. Mounting it
// at the top would short-circuit anonymous visitors who should still
// reach login / register / public play paths.
func RequireVerifiedEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PlayerFromContext(r.Context())
		if !ok || p.IsEmailVerified() || p.Email == "" {
			next.ServeHTTP(w, r)

			return
		}
		// HTMX swaps the response body into the triggering element, so a
		// 303 here would inject the pending page into whatever partial
		// target fired the request. HX-Redirect tells htmx to do a
		// top-level navigation instead.
		if r.Header.Get("Hx-Request") == "true" {
			w.Header().Set("Hx-Redirect", verifyPendingPath)
			w.WriteHeader(http.StatusNoContent)

			return
		}
		http.Redirect(w, r, verifyPendingPath, http.StatusSeeOther)
	})
}

// HandleVerifyPending renders the interstitial. The handler reads the
// session player itself rather than relying on PlayerFromContext: the
// route is not wrapped by the standard auth middleware (which would
// itself bounce here for unverified players, looping). Unauthenticated
// visitors are 303'd to /login. Already-verified visitors are 303'd to
// their role landing so an old bookmark does not strand them on the
// interstitial.
func HandleVerifyPending(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	flash *SignedFlash,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/verify_email_pending.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := AuthenticatedSessionPlayer(r, players, sessions)
		if !ok {
			redirectToLoginWithNext(w, r)

			return
		}
		if p.IsEmailVerified() || p.Email == "" {
			// Drain any in-flight flash cookie before bouncing so the
			// one-shot banner does not survive past its intended page.
			// p.Email == "" cannot complete the resend flow either, so
			// the interstitial would be a dead end - send them to the
			// landing instead.
			flash.Read(w, r)
			http.Redirect(w, r, landingPathFor(p.Role), http.StatusSeeOther)

			return
		}

		data := verifyPendingData{Title: "Verify email", Email: p.Email}
		if fr := flash.Read(w, r); fr.OK {
			data.Notice = fr.Notice
			data.Error = fr.Err
			data.ResendWaitSeconds = fr.WaitSeconds
			data.ResendDisabled = fr.WaitSeconds > 0
		}
		render.render(w, r, http.StatusOK, data)
	})
}

// HandleVerifyResend issues a fresh verification email for the
// signed-in player. Uses PRG (303-then-GET) so a refresh does not
// re-send. The per-IP rate limiter is the abuse cap; the per-session
// guard (must be signed-in + unverified) is the authorisation gate.
// Errors all flow back through a flash banner on the interstitial so
// the user always sees one page after the form post.
func HandleVerifyResend(
	logger *slog.Logger,
	players PlayerStore,
	sessions *session.Manager,
	tokens VerifyTokenStore,
	sender VerifyEmailSender,
	baseURL string,
	limiter *VerifyResendLimiter,
	flash *SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := AuthenticatedSessionPlayer(r, players, sessions)
		if !ok {
			redirectToLoginWithNext(w, r)

			return
		}
		if p.IsEmailVerified() {
			http.Redirect(w, r, landingPathFor(p.Role), http.StatusSeeOther)

			return
		}
		if p.Email == "" {
			// Should not happen for password registrants since #111 PR1,
			// but guard so an OAuth-stub row without an email cannot
			// trigger a panic in SendVerifyEmail.
			flash.SetError(w, "No email on file. Sign out and register again.", 0)
			http.Redirect(w, r, verifyPendingPath, http.StatusSeeOther)

			return
		}

		if wait, allowed := limiter.Allow(limiter.ClientIP(r)); !allowed {
			// Round sub-second remainders up so a 0.4s wait still
			// reports as 1s rather than "now"; otherwise Retry-After:
			// 0 (and ResendDisabled=false) lets a scripted client
			// retry-loop the rate limiter.
			seconds := int((wait + time.Second - 1) / time.Second)
			flash.SetError(w, "Slow down: wait a moment before requesting another email.", seconds)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Redirect(w, r, verifyPendingPath, http.StatusSeeOther)

			return
		}

		// Detach from r.Context() so a client disconnect mid-SMTP does
		// not cancel the send: the limiter already stamped this IP, so
		// a cancelled attempt would burn the next 60-second window
		// without ever delivering. The bounded timeout keeps a stuck
		// SMTP server from pinning the goroutine indefinitely.
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), verifyEmailDispatchTimeout)
		defer cancel()
		if err := SendVerifyEmail(sendCtx, tokens, sender, baseURL,
			p.Email, p.ID, time.Now().UTC()); err != nil {
			logger.WarnContext(r.Context(), "verify resend dispatch failed",
				slog.Int64("player_id", p.ID), slog.Any("err", err))
			flash.SetError(w, "Could not send the email right now. Try again in a moment.", 0)
			http.Redirect(w, r, verifyPendingPath, http.StatusSeeOther)

			return
		}

		flash.SetNotice(w, "Verification email sent. Check your inbox.")
		http.Redirect(w, r, verifyPendingPath, http.StatusSeeOther)
	})
}

// AuthenticatedSessionPlayer returns the credentialled player the
// request's session points at, reporting false when the cookie is
// missing, invalid, version-stale, points at a deleted row, or resolves
// to an anonymous-only row. It builds on loadSessionPlayer so the
// session_version check - the single definition of a live session -
// lives in one place (#620); this layer only adds the "must be
// credentialled" rule its callers share: the interstitial + resend
// handlers, the /login redirect, and the home-page footer.
func AuthenticatedSessionPlayer(r *http.Request, players PlayerStore, sessions *session.Manager) (*Player, bool) {
	p, err := loadSessionPlayer(r, players, sessions)
	if err != nil || !p.IsAuthenticated() {
		return nil, false
	}

	return p, true
}

// VerifyResendLimiter is a per-IP cool-down. Concurrency-safe; the map
// is pruned of stale entries every Allow call so memory stays
// proportional to the live caller set rather than the lifetime set.
//
// trustedProxyCIDRs is the allow-list of upstream proxies whose
// X-Forwarded-For header [VerifyResendLimiter.ClientIP] honours when
// bucketing; nil means "trust nothing" so XFF is ignored and the
// bucket key is the request peer's address. See [request.ClientIP]
// for the walk semantics and #463 for the rationale.
type VerifyResendLimiter struct {
	mu                sync.Mutex
	last              map[string]time.Time
	window            time.Duration
	now               func() time.Time
	trustedProxyCIDRs []*net.IPNet
}

// NewVerifyResendLimiter returns a limiter using the supplied window,
// [time.Now] as the clock, and trustedProxyCIDRs as the per-IP bucket
// override list. nil/empty CIDR slice disables the XFF walk; see
// [VerifyResendLimiter] for the policy. The clock is injectable via
// the export_test seam so tests can fast-forward without sleeping.
func NewVerifyResendLimiter(window time.Duration, trustedProxyCIDRs []*net.IPNet) *VerifyResendLimiter {
	return &VerifyResendLimiter{
		last:              map[string]time.Time{},
		window:            window,
		now:               time.Now,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}
}

// ClientIP resolves the per-IP bucket key from r using the
// trustedProxyCIDRs allow-list passed at construction. HTTP handlers
// pass the result to [VerifyResendLimiter.Allow]; the unit tests that
// pin Allow itself keep using Allow + a synthetic IP.
func (l *VerifyResendLimiter) ClientIP(r *http.Request) string {
	return request.ClientIP(r, l.trustedProxyCIDRs)
}

// Allow reports whether ip may resend right now. On admit, stamps the
// bucket so the next call within the window is blocked. On block,
// returns the remaining wait so the caller can render it.
func (l *VerifyResendLimiter) Allow(ip string) (time.Duration, bool) {
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
