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
	"github.com/starquake/topbanana/internal/request"
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
// snapshot; Notice / Error surface the one-shot banner HandleEmailGet
// reads from the flash cookie after a PRG-redirected send attempt.
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

// EmailRateLimiter is a per-IP cool-down for the test-send button.
// Safe for concurrent use. Allow atomically reports + stamps so two
// concurrent callers cannot both observe "allowed"; Cancel reverts a
// specific stamp (matched against the token Allow returned) so a
// recipient-validation rejection does not burn the next window.
//
// trustedProxyCIDRs is the allow-list of upstream proxies whose
// X-Forwarded-For header [EmailRateLimiter.ClientIP] honours when
// bucketing; nil means "trust nothing" so XFF is ignored and the
// bucket key is the request peer's address. See [request.ClientIP]
// for the walk semantics and #463 for the rationale.
type EmailRateLimiter struct {
	mu                sync.Mutex
	last              map[string]time.Time
	window            time.Duration
	nowFunc           func() time.Time
	trustedProxyCIDRs []*net.IPNet
}

// NewEmailRateLimiter returns a limiter that allows one POST per
// window per source IP. The clock defaults to [time.Now] in
// production; tests inject a deterministic clock via the export_test
// helper. trustedProxyCIDRs is the upstream-proxy allow-list passed
// to [request.ClientIP] when [EmailRateLimiter.ClientIP] resolves
// the bucket key; nil disables the XFF walk.
func NewEmailRateLimiter(window time.Duration, trustedProxyCIDRs []*net.IPNet) *EmailRateLimiter {
	return newEmailRateLimiterWithClock(window, time.Now, trustedProxyCIDRs)
}

func newEmailRateLimiterWithClock(
	window time.Duration, now func() time.Time, trustedProxyCIDRs []*net.IPNet,
) *EmailRateLimiter {
	return &EmailRateLimiter{
		last:              make(map[string]time.Time),
		window:            window,
		nowFunc:           now,
		trustedProxyCIDRs: trustedProxyCIDRs,
	}
}

// ClientIP resolves the per-IP bucket key from r using the
// trustedProxyCIDRs allow-list passed at construction. Exposed so the
// HTTP handler can stamp + cancel using the same key without
// resolving the IP twice in two distinct call sites.
func (l *EmailRateLimiter) ClientIP(r *http.Request) string {
	return request.ClientIP(r, l.trustedProxyCIDRs)
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

// HandleEmailGet renders /admin/email: status panel + log + send form.
// CSRF lives on the matching POST route in routes.go. flash carries
// the one-shot banner from POST /admin/email/test's 303 to here (#321).
func HandleEmailGet(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mailerService EmailRecorder,
	status mailer.StatusView,
	flash *EmailFlash,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notice, errMsg, wait := readEmailFlash(w, r, flash)
		data := buildEmailPageData(r, mailerService, status, notice, errMsg)
		data.RateLimitWait = wait
		render.Render(w, r, http.StatusOK, data)
	})
}

// readEmailFlash returns the banner fields; flash.Read clears the cookie.
func readEmailFlash(w http.ResponseWriter, r *http.Request, flash *EmailFlash) (notice, errMsg string, wait int) {
	fr := flash.Read(w, r)
	if !fr.OK {
		return "", "", 0
	}
	if fr.Kind == FlashNotice {
		return fr.Msg, "", 0
	}

	return "", fr.Msg, fr.Wait
}

// HandleEmailTest handles POST /admin/email/test. Empty recipient
// falls back to the signed-in admin's email. Every response 303s to
// /admin/email with a one-shot flash; PRG keeps Firefox from
// prompting on refresh (#321). Recipient-validation failures roll the
// rate-limit stamp back via Cancel so a typo doesn't burn the window.
// The form is parsed by csrfMW before this handler runs.
func HandleEmailTest(
	logger *slog.Logger,
	mailerService EmailRecorder,
	limiter *EmailRateLimiter,
	flash *EmailFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := limiter.ClientIP(r)
		token, blocked := rateLimited(w, r, limiter, ip, flash)
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
			flash.SetError(w, "Recipient is not a valid email address.", 0)
			http.Redirect(w, r, "/admin/email", http.StatusSeeOther)

			return
		}
		if !ok {
			limiter.Cancel(ip, token)
			flash.SetError(w, "Enter a recipient email address - your account has no email on file.", 0)
			http.Redirect(w, r, "/admin/email", http.StatusSeeOther)

			return
		}

		// Recipient validated; keep the Allow stamp so subsequent
		// clicks within the window get rate-limited as intended.
		sendTestAndRedirect(w, r, logger, mailerService, flash, to)
	})
}

// HandleEmailTestRefresh handles direct GETs to /admin/email/test
// (stale bookmark, manual URL entry). Without it Go's ServeMux would
// return 405 for the method mismatch; the 303 keeps the URL
// recoverable. The PRG flow already redirects POSTs to /admin/email,
// so this is a defensive fallback rather than the primary path.
func HandleEmailTestRefresh() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/email", http.StatusSeeOther)
	})
}

// rateLimited applies the limiter and stashes a "Slow down" flash +
// Retry-After on a hit. Returned token is the Allow stamp; pass to
// Cancel if the caller bails out before dispatching. blocked=true
// means a 303 was already written.
func rateLimited(
	w http.ResponseWriter,
	r *http.Request,
	limiter *EmailRateLimiter,
	ip string,
	flash *EmailFlash,
) (time.Time, bool) {
	ok, wait, token := limiter.Allow(ip)
	if ok {
		return token, false
	}
	seconds := max(int(wait.Round(time.Second).Seconds()), 1)
	flash.SetError(w, "Slow down: wait a moment before sending another test email.", seconds)
	// Retry-After alongside the human-readable banner: humans see the
	// flash on the follow-up GET; scripted callers honour the header.
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Redirect(w, r, "/admin/email", http.StatusSeeOther)

	return time.Time{}, true
}

// sendTestAndRedirect dispatches the send, stashes the banner, and
// 303s. PRG keeps refresh safe (#321).
func sendTestAndRedirect(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	mailerService EmailRecorder,
	flash *EmailFlash,
	to string,
) {
	err := mailer.SendTest(r.Context(), mailerService, to)
	switch {
	case err == nil:
		flash.SetNotice(w, "Test email sent to "+to+".")
	case errors.Is(err, mailer.ErrNotConfigured):
		flash.SetError(
			w,
			"Email is not configured on this instance - set SMTP_HOST, SMTP_PORT, and SMTP_FROM to enable sending.",
			0,
		)
	default:
		// Verbatim SMTP error so the operator can debug "550 mailbox unavailable" etc. directly (#321).
		logger.InfoContext(r.Context(), "test email send failed", slog.Any("err", err))
		flash.SetError(w, "Send failed: "+err.Error(), 0)
	}
	http.Redirect(w, r, "/admin/email", http.StatusSeeOther)
}

// resolveTestRecipient picks the recipient for the test send.
// (addr, true, true): explicit value parsed, or blank fell back to the
// admin's email. ("", false, true): blank with no admin email on file.
// ("", false, false): explicit value but ParseAddress rejected it - we
// never silently fall back to the admin's address in this case so a
// typo does not silently dispatch elsewhere.
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
