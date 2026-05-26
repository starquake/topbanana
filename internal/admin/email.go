package admin

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/mailer"
)

// EmailRecorder is the subset of the mailer the admin email
// diagnostics page interacts with. Send drives the test-mail button;
// Recent backs the "Recent send log" table. Both methods are
// implemented by [mailer.Tester]; the interface lives here so the
// handler tests can stand up a stub without spinning the real SMTP
// path.
type EmailRecorder interface {
	Send(ctx context.Context, msg mailer.Message) error
	Recent(n int) []mailer.LogEntry
}

// EmailTestRateLimit is the per-IP minimum gap between consecutive
// POST /admin/email/test calls. The diagnostics button is meant for
// occasional debugging, so a 10-second cool-down keeps a stuck admin
// (or an automated probe) from turning the page into an outbound mail
// floodgate (#321).
const EmailTestRateLimit = 10 * time.Second

// MaxFormSizeMiddleware wraps r.Body in [http.MaxBytesReader] before
// the next handler (or any other middleware that calls ParseForm) runs,
// capping the bytes a single form submission can consume. The CSRF
// middleware calls ParseForm during validation; without this wrapper
// running first, a malicious caller could ship an unbounded body and
// the CSRF layer would happily slurp it before the handler ever sees
// the request. Mount this in front of csrfMW on form-driven POSTs.
func MaxFormSizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		next.ServeHTTP(w, r)
	})
}

// emailLogDisplayLimit is the number of ring-buffer entries the
// template renders. Pinned to the mailer ring buffer's capacity so
// the page always shows the full available history.
const emailLogDisplayLimit = mailer.LogCapacity

// emailPageData backs the email.gohtml template. Status is the safe
// view of SMTP config; LogEntries is the newest-first ring-buffer
// snapshot; Notice / Error surface the one-shot banners HandleEmailTest
// renders inline after a send attempt.
type emailPageData struct {
	Title         string
	Status        mailer.StatusView
	LogEntries    []emailLogRow
	Notice        string
	Error         string
	DefaultTo     string
	RateLimitWait int
}

// emailLogRow is the render-time shape of one ring-buffer entry. The
// timestamp is preformatted in UTC so the template stays declarative,
// and Success is the pre-derived "no Err string" flag the row uses to
// pick its colour.
type emailLogRow struct {
	SentAt  string
	To      string
	Subject string
	Kind    string
	Success bool
	Err     string
}

// EmailRateLimiter tracks the last successful test-send per source IP
// so HandleEmailTest can reject too-frequent clicks without standing
// up an out-of-process limiter. The map grows by one entry per
// distinct admin IP; cardinality stays small in practice because the
// /admin/email route is admin-gated. Concurrent admins coordinating
// from the same NAT share a bucket; the mutex keeps the read/write
// pair atomic.
//
// Safe for concurrent use: every public method takes l.mu so callers
// can drive Allow / Cancel from multiple goroutines (the integration
// test exercises exactly this) without external synchronisation.
//
// The limiter exposes two operations: Allow reports whether a send is
// permitted right now AND stamps the bucket atomically when it is,
// returning the stamp it wrote as a token so two concurrent callers
// can never both observe "allowed". Cancel reverts a specific stamp
// (matched against the token) so a 400 recipient-validation failure
// does not burn the next 10-second window, which would otherwise
// prevent the admin from immediately re-submitting the form with a
// corrected address. Matching on the token keeps Cancel from
// clobbering a newer stamp written by a second concurrent caller in
// between this caller's Allow and Cancel.
type EmailRateLimiter struct {
	mu      sync.Mutex
	last    map[string]time.Time
	window  time.Duration
	nowFunc func() time.Time
}

// NewEmailRateLimiter returns a limiter that allows one POST per
// window per source IP. The clock defaults to [time.Now] in
// production; tests inject a deterministic clock via the export_test
// helper.
func NewEmailRateLimiter(window time.Duration) *EmailRateLimiter {
	return newEmailRateLimiterWithClock(window, time.Now)
}

func newEmailRateLimiterWithClock(window time.Duration, now func() time.Time) *EmailRateLimiter {
	return &EmailRateLimiter{
		last:    make(map[string]time.Time),
		window:  window,
		nowFunc: now,
	}
}

// Allow reports whether ip is permitted to send right now and stamps
// the bucket under the same lock acquisition on the allow path so two
// concurrent callers cannot both observe "allowed". When the call is
// admitted the returned token is the timestamp written into the
// bucket; pass it to [EmailRateLimiter.Cancel] to revert this specific
// stamp on a downstream validation failure. When the bucket is hot,
// retryAfter is the duration the caller should wait before the next
// attempt, the token is the zero time, and the existing stamp is
// left untouched.
func (l *EmailRateLimiter) Allow(ip string) (bool, time.Duration, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked()

	now := l.nowFunc()
	if prev, ok := l.last[ip]; ok {
		if elapsed := now.Sub(prev); elapsed < l.window {
			return false, l.window - elapsed, time.Time{}
		}
	}
	l.last[ip] = now

	return true, 0, now
}

// Cancel reverts the stamp Allow wrote for ip, but only if the live
// stamp still matches token. Matching on token keeps a slow caller
// from clobbering a newer stamp a second concurrent caller wrote in
// between this caller's Allow and Cancel - that newer stamp must
// stand so the second caller's window is honoured. Idempotent: calling
// Cancel on an ip with no entry, or with a stale token, is a no-op.
func (l *EmailRateLimiter) Cancel(ip string, token time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if cur, ok := l.last[ip]; ok && cur.Equal(token) {
		delete(l.last, ip)
	}
}

// pruneLocked drops map entries older than 2 * window so the limiter's
// memory footprint stays proportional to the live caller set rather
// than the lifetime caller set. Caller must hold l.mu. Cheap given the
// admin-gated cardinality - a single sweep is fine here.
func (l *EmailRateLimiter) pruneLocked() {
	cutoff := l.nowFunc().Add(-2 * l.window)
	for ip, ts := range l.last {
		if ts.Before(cutoff) {
			delete(l.last, ip)
		}
	}
}

// HandleEmailGet renders /admin/email. The page is a status panel +
// recent-send-log + test-send form; nothing in here mutates state, so
// CSRF protection lives on the matching POST route in routes.go and
// not here. The form's hidden csrf_token field is populated by the
// renderer's prepared {{csrfToken}} func.
func HandleEmailGet(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mailerService EmailRecorder,
	status mailer.StatusView,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.Render(w, r, http.StatusOK, buildEmailPageData(r, mailerService, status, "", ""))
	})
}

// HandleEmailTest handles POST /admin/email/test. The recipient input
// is optional; an empty value falls back to the admin's own email
// address (or surfaces a "set a recipient" hint if the admin has no
// email on file). The response is a full page render with an inline
// banner; we deliberately do not 303-redirect so the verbatim SMTP
// error (the #321 design decision) cannot be lost in a query-string
// truncation.
//
// Per-IP rate limit keeps the button from being abused: a too-soon
// retry surfaces a 429 with the standard Retry-After hint and an
// inline banner. Allow stamps the bucket atomically so two concurrent
// clicks cannot both pass; a recipient-validation failure rolls the
// stamp back via [EmailRateLimiter.Cancel] so a typo'd address does
// not burn the next window.
//
// The form is parsed by [MaxFormSizeMiddleware] before this handler
// runs, so r.PostFormValue is already populated.
func HandleEmailTest(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mailerService EmailRecorder,
	status mailer.StatusView,
	limiter *EmailRateLimiter,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		token, blocked := rateLimited(w, r, limiter, ip, render, mailerService, status)
		if blocked {
			return
		}

		to, ok, valid := resolveTestRecipient(r)
		if !valid {
			// Bail out on a bad recipient: roll back the stamp Allow
			// took so the admin can retry immediately with a corrected
			// address rather than waiting out the window. Cancel
			// matches on token so a second concurrent caller's newer
			// stamp is not clobbered here.
			limiter.Cancel(ip, token)
			data := buildEmailPageData(
				r, mailerService, status, "",
				"Recipient is not a valid email address.",
			)
			render.Render(w, r, http.StatusBadRequest, data)

			return
		}
		if !ok {
			limiter.Cancel(ip, token)
			data := buildEmailPageData(
				r, mailerService, status, "",
				"Enter a recipient email address - your account has no email on file.",
			)
			render.Render(w, r, http.StatusBadRequest, data)

			return
		}

		// Recipient validated; keep the Allow stamp so subsequent
		// clicks within the window get rate-limited as intended.
		renderTestSendResult(w, r, logger, render, mailerService, status, to)
	})
}

// HandleEmailTestRefresh handles GET /admin/email/test. Browsers land
// here when an admin refreshes the page after a test-send POST (the
// form action is /admin/email/test), and Go's ServeMux returns 405 for
// a method-mismatched route by default. Redirecting to /admin/email is
// the gentlest landing - the inline banner is lost on refresh, which
// is acceptable; the alternative (carrying the verbatim SMTP error
// through a query string) was rejected in the #321 design comment
// because the truncation rules vary across user agents.
func HandleEmailTestRefresh() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/email", http.StatusSeeOther)
	})
}

// rateLimited applies the per-IP rate limiter; on a hit it renders the
// page at 429 with a Retry-After header and reports blocked=true so
// the caller can early-return. Allow stamps the bucket atomically on
// the permitted path so two concurrent clicks cannot both pass; the
// returned token is the stamp Allow wrote and must be passed back to
// [EmailRateLimiter.Cancel] if the caller bails out before dispatching
// a send. On the blocked path the token is the zero time.
func rateLimited(
	w http.ResponseWriter,
	r *http.Request,
	limiter *EmailRateLimiter,
	ip string,
	render *TemplateRenderer,
	mailerService EmailRecorder,
	status mailer.StatusView,
) (time.Time, bool) {
	ok, wait, token := limiter.Allow(ip)
	if ok {
		return token, false
	}
	seconds := max(int(wait.Round(time.Second).Seconds()), 1)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	data := buildEmailPageData(
		r, mailerService, status, "",
		"Slow down: wait a moment before sending another test email.",
	)
	data.RateLimitWait = seconds
	render.Render(w, r, http.StatusTooManyRequests, data)

	return time.Time{}, true
}

// renderTestSendResult dispatches the test send and renders the right
// banner + HTTP status. Pulled out of HandleEmailTest so the handler
// stays under revive's function-length limit and the three branches
// (success / not-configured / verbatim SMTP error) read flat.
func renderTestSendResult(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	render *TemplateRenderer,
	mailerService EmailRecorder,
	status mailer.StatusView,
	to string,
) {
	err := mailer.SendTest(r.Context(), mailerService, to)
	switch {
	case err == nil:
		data := buildEmailPageData(r, mailerService, status, "Test email sent to "+to+".", "")
		render.Render(w, r, http.StatusOK, data)
	case errors.Is(err, mailer.ErrNotConfigured):
		// Surface the sentinel as a 503 banner rather than a 500.
		// The admin needs to know SMTP is unwired; the diagnostics
		// page is exactly where to tell them.
		data := buildEmailPageData(
			r, mailerService, status, "",
			"Email is not configured on this instance - set SMTP_HOST, SMTP_PORT, and SMTP_FROM to enable sending.",
		)
		render.Render(w, r, http.StatusServiceUnavailable, data)
	default:
		// Verbatim SMTP error so the operator can debug "550 mailbox
		// unavailable" or "TLS handshake failed" directly (#321).
		logger.InfoContext(r.Context(), "test email send failed", slog.Any("err", err))
		data := buildEmailPageData(r, mailerService, status, "", "Send failed: "+err.Error())
		render.Render(w, r, http.StatusOK, data)
	}
}

// resolveTestRecipient picks the email address the test send targets
// and reports whether the request looks usable. The three return
// values pin the cases the handler renders separately:
//
//   - ("addr", true,  true)  - explicit form value parsed cleanly, or
//     blank form fell back to the signed-in admin's email.
//   - ("",     false, true)  - blank form and the admin has no email
//     on file; the handler renders a "set a recipient" hint at 400.
//   - ("",     false, false) - explicit form value but [mail.ParseAddress]
//     rejected it; the handler renders a "not a valid email" hint at
//     400. We deliberately do NOT silently fall back to the admin's
//     own address in this case - the admin clearly meant the form
//     value, dispatching elsewhere would be a surprise.
//
// The form is parsed by [MaxFormSizeMiddleware] before the handler
// runs, so r.PostFormValue is already populated.
//
// to read with names than with a comment above the return statement.
//
//nolint:nonamedreturns // three booleans next to a string are easier
func resolveTestRecipient(r *http.Request) (addr string, ok, valid bool) {
	raw := strings.TrimSpace(r.PostFormValue("to"))
	if raw != "" {
		if _, perr := mail.ParseAddress(raw); perr != nil {
			return "", false, false
		}

		return raw, true, true
	}
	// Fall back to the signed-in admin's email address. The admin
	// gate above already populated the context with their player row
	// so PlayerFromContext is a hit in every legitimate request.
	if p, ok := auth.PlayerFromContext(r.Context()); ok && p.Email != "" {
		return p.Email, true, true
	}

	return "", false, true
}

// clientIP extracts the source IP the rate limiter buckets on. Strips
// the port from RemoteAddr so "1.2.3.4:5678" and "1.2.3.4:9999" share
// a bucket. X-Forwarded-For is intentionally NOT consulted: the
// deployment exposes the server directly today, and a forged XFF
// header would let an attacker pick any bucket and burn / bypass it.
// When a future deployment puts a trusted reverse proxy in front, the
// signed-XFF allow-list can be added behind a TRUSTED_PROXY_IPS config
// knob - until then RemoteAddr is the only IP we can attribute.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// buildEmailPageData is the common page-data assembly. The handler
// passes the live status + the optional banner copy; the helper
// pulls the ring buffer entries and the signed-in admin's email so
// the form has a sensible default recipient.
func buildEmailPageData(
	r *http.Request,
	mailerService EmailRecorder,
	status mailer.StatusView,
	notice, errMsg string,
) emailPageData {
	defaultTo := ""
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		defaultTo = p.Email
	}

	return emailPageData{
		Title:      "Admin Dashboard - Email",
		Status:     status,
		LogEntries: snapshotLog(mailerService),
		Notice:     notice,
		Error:      errMsg,
		DefaultTo:  defaultTo,
	}
}

// snapshotLog reads the newest-first ring buffer and translates each
// entry into the render-time row shape. Renders timestamps as RFC3339
// UTC so the diagnostics page sorts consistently regardless of the
// server's local timezone.
func snapshotLog(mailerService EmailRecorder) []emailLogRow {
	raw := mailerService.Recent(emailLogDisplayLimit)
	out := make([]emailLogRow, 0, len(raw))
	for _, entry := range raw {
		out = append(out, emailLogRow{
			SentAt:  entry.SentAt.UTC().Format(time.RFC3339),
			To:      entry.To,
			Subject: entry.Subject,
			Kind:    string(entry.Kind),
			Success: entry.Err == "",
			Err:     entry.Err,
		})
	}

	return out
}
