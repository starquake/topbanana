package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
)

// verifyEmailRequestPath is the route for the public self-service
// "resend my verification link" form. Constant so the handler, the
// flash cookie path, and the discovery affordances share one string.
const verifyEmailRequestPath = "/verify-email/request"

// verifyRequestSuccessMsg is the account-existence-opaque flash the
// POST handler always sets, identical in shape to the forgot-password
// banner. Phrased so an attacker probing a list of addresses cannot
// tell from the response whether any given address is registered or
// already verified.
const verifyRequestSuccessMsg = "If an account matches, we've sent a verification link to its email."

// verifyEmailRequestData backs the verify_email_request.gohtml template.
type verifyEmailRequestData struct {
	Title          string
	Email          string
	Notice         string
	Error          string
	SubmitDisabled bool
	SubmitWaitSecs int
}

// HandleVerifyEmailRequestForm renders GET /verify-email/request. The
// form asks for an email address and posts to the same path. A
// signed-in visitor is redirected to their role landing - the
// in-session resend on /verify-email/pending is the faster path for
// them.
func HandleVerifyEmailRequestForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	flash *SignedFlash,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/verify_email_request.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}

		data := verifyEmailRequestData{Title: "Resend verification email"}
		if fr := flash.Read(w, r); fr.OK {
			data.Notice = fr.Notice
			data.Error = fr.Err
			data.SubmitWaitSecs = fr.WaitSeconds
			data.SubmitDisabled = fr.WaitSeconds > 0
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// VerifyRequestDispatchDeps bundles the verify-email dispatch dependencies so
// HandleVerifyEmailRequestSubmit stays under revive's argument limit (same
// packaging the register / login handlers use). Tasks tracks the detached send
// so a graceful shutdown drains it before the DB closes (#741); it may be nil,
// in which case the dispatch runs untracked.
type VerifyRequestDispatchDeps struct {
	Tokens  VerifyTokenStore
	Sender  VerifyEmailSender
	BaseURL string
	Tasks   *bgtasks.Tracker
}

// HandleVerifyEmailRequestSubmit handles POST /verify-email/request.
// Account-existence-opaque: identical 303 + flash regardless of whether
// the submitted address is registered, already verified, or unknown.
// The SMTP send (when a match is found and the row is still unverified)
// runs on a detached goroutine so response timing is independent of
// whether mail was actually sent.
func HandleVerifyEmailRequestSubmit(
	logger *slog.Logger,
	players PlayerStore,
	sessions *session.Manager,
	dispatch VerifyRequestDispatchDeps,
	limiter *VerifyResendLimiter,
	flash *SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "verify-email request form parse failed", slog.Any("err", err))
			flash.SetError(w, "Your submission was not understood. Try again.", 0)
			http.Redirect(w, r, verifyEmailRequestPath, http.StatusSeeOther)

			return
		}

		if wait, allowed := limiter.Allow(limiter.ClientIP(r)); !allowed {
			seconds := int((wait + time.Second - 1) / time.Second)
			flash.SetError(w, "Slow down: wait a moment before submitting again.", seconds)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Redirect(w, r, verifyEmailRequestPath, http.StatusSeeOther)

			return
		}

		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		if email != "" {
			dispatchVerifyRequestIfMatch(r.Context(), logger, players, dispatch, email)
		}

		flash.SetNotice(w, verifyRequestSuccessMsg)
		http.Redirect(w, r, verifyEmailRequestPath, http.StatusSeeOther)
	})
}

// dispatchVerifyRequestIfMatch looks up the email and, when the row
// exists with a non-empty email AND is still unverified, detaches a
// goroutine that mints+sends a fresh verify link. Lookup misses and
// already-verified rows (which covers OAuth-linked rows, whose email
// is verified-by-construction at link time) are silently ignored so
// the response timing is independent of the lookup result.
func dispatchVerifyRequestIfMatch(
	ctx context.Context,
	logger *slog.Logger,
	players PlayerStore,
	dispatch VerifyRequestDispatchDeps,
	email string,
) {
	p, err := players.GetPlayerByEmail(ctx, email)
	if err != nil || p.Email == "" || p.IsEmailVerified() {
		return
	}
	// Detached so SMTP latency is not observable from response timing -
	// the account-existence-opaque contract depends on every request
	// returning in roughly the same wall-clock time. The bounded timeout
	// keeps a stuck SMTP server from pinning the goroutine. The send is
	// tracked so shutdown drains it before the DB closes (#741).
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyEmailDispatchTimeout)
	dispatch.Tasks.Go(func() {
		defer cancel()
		if sendErr := SendVerifyEmail(sendCtx, dispatch.Tokens, dispatch.Sender, dispatch.BaseURL,
			p.Email, p.ID, time.Now().UTC()); sendErr != nil {
			logger.WarnContext(sendCtx, "verify-email request dispatch failed",
				slog.Int64("player_id", p.ID), slog.Any("err", sendErr))
		}
	})
}
