package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
)

// forgotPasswordCooldown is the per-IP gap between consecutive POSTs
// to /forgot-password. Long enough that a scripted enumeration cannot
// probe accounts via timing, short enough that a user who mistyped
// their email and wants to retry does not wait forever.
const forgotPasswordCooldown = 60 * time.Second

// ForgotPasswordCooldown exposes the per-IP cool-down so the wiring
// layer can build a [VerifyResendLimiter] with the same window the
// handler logs against. Same pattern VerifyResendCooldown uses.
func ForgotPasswordCooldown() time.Duration { return forgotPasswordCooldown }

// forgotPasswordSuccessMsgKey is the catalog key for the
// account-existence-opaque flash the POST handler always sets on success
// or no-match. The phrasing deliberately does not confirm a match: an
// attacker who probes a list of emails cannot tell from the response or
// the timing whether any given address is registered.
const forgotPasswordSuccessMsgKey = "forgotPassword.sentNotice"

// forgotPageData backs the forgot-password.gohtml template.
type forgotPageData struct {
	Title          string
	Identifier     string
	Notice         string
	Error          string
	SubmitDisabled bool
	SubmitWaitSecs int
}

// HandleForgotForm renders GET /forgot-password. The form asks for a
// displayName or email and posts to the same path. An already-signed-in
// visitor is redirected to their role landing so the password-reset
// flow is reserved for users who cannot log in.
func HandleForgotForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	flash *SignedFlash,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/forgot_password.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}

		data := forgotPageData{Title: locale.Translate(locale.Resolve(r), "forgotPassword.title")}
		if fr := flash.Read(w, r); fr.OK {
			data.Notice = fr.Notice
			data.Error = fr.Err
			data.SubmitWaitSecs = fr.WaitSeconds
			data.SubmitDisabled = fr.WaitSeconds > 0
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// ForgotDispatchDeps bundles the reset-email dispatch dependencies so
// HandleForgotSubmit stays under revive's argument limit (same packaging the
// register / login handlers use). Tasks tracks the detached send so a graceful
// shutdown drains it before the DB closes (#740); it may be nil, in which case
// the dispatch runs untracked.
type ForgotDispatchDeps struct {
	Tokens  ResetTokenStore
	Sender  VerifyEmailSender
	BaseURL string
	Tasks   *bgtasks.Tracker
}

// HandleForgotSubmit handles POST /forgot-password. Always responds
// the same way regardless of whether a matching account exists, so an
// attacker cannot enumerate registered emails by parsing the response.
// The SMTP send (when a match is found) runs on a detached goroutine
// so a client disconnect cannot burn the rate-limit window without
// dispatching, and so response timing is independent of whether mail
// was actually sent.
func HandleForgotSubmit(
	logger *slog.Logger,
	players PlayerStore,
	sessions *session.Manager,
	dispatch ForgotDispatchDeps,
	limiter *VerifyResendLimiter,
	flash *SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "forgot-password form parse failed", slog.Any("err", err))
			flash.SetError(w, locale.Translate(locale.Resolve(r), "common.submissionNotUnderstood"), 0)
			http.Redirect(w, r, "/forgot-password", http.StatusSeeOther)

			return
		}

		if wait, allowed := limiter.Allow(limiter.ClientIP(r)); !allowed {
			seconds := int((wait + time.Second - 1) / time.Second)
			flash.SetError(w, locale.Translate(locale.Resolve(r), "common.slowDownSubmit"), seconds)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Redirect(w, r, "/forgot-password", http.StatusSeeOther)

			return
		}

		identifier := strings.TrimSpace(r.PostFormValue("identifier"))
		if identifier != "" {
			dispatchForgotIfMatch(r.Context(), logger, players, dispatch, identifier)
		}

		// Always flash the same success message - never reveal whether
		// the identifier matched a real account.
		flash.SetNotice(w, locale.Translate(locale.Resolve(r), forgotPasswordSuccessMsgKey))
		http.Redirect(w, r, "/forgot-password", http.StatusSeeOther)
	})
}

// dispatchForgotIfMatch looks the identifier up by displayName then
// email, and (when found and the player has an email on file)
// detaches a goroutine that mints+sends a reset link. Lookup misses
// and OAuth-only rows are silently ignored so the timing of the
// response is independent of the lookup result.
func dispatchForgotIfMatch(
	ctx context.Context,
	logger *slog.Logger,
	players PlayerStore,
	dispatch ForgotDispatchDeps,
	identifier string,
) {
	p, ok := resolveForgotIdentifier(ctx, players, identifier)
	if !ok || p.Email == "" {
		return
	}
	// Detach so the SMTP latency is not observable from the response
	// timing - account-existence-opaqueness depends on every request
	// returning in roughly the same wall-clock time. The detached
	// context inherits cancellation safety only; it cannot be aborted
	// by the client closing their tab. The send is tracked so shutdown
	// drains it before the DB closes (#740).
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyEmailDispatchTimeout)
	dispatch.Tasks.Go(func() {
		defer cancel()
		if err := SendResetEmail(sendCtx, dispatch.Tokens, dispatch.Sender, dispatch.BaseURL,
			p.Email, p.ID, time.Now().UTC()); err != nil {
			logger.WarnContext(sendCtx, "forgot-password dispatch failed",
				slog.Int64("player_id", p.ID), slog.Any("err", err))
		}
	})
}

// resolveForgotIdentifier returns the player matching identifier
// (treated as a displayName first, then as an email) or (nil, false) if
// neither lookup hits. Errors other than "not found" are swallowed at
// info level - the account-existence-opaque contract means we cannot
// surface a transient DB hiccup to the user, and the rate limiter
// caps the abuse blast radius.
func resolveForgotIdentifier(
	ctx context.Context, players PlayerStore, identifier string,
) (*Player, bool) {
	if p, err := players.GetPlayerByDisplayName(ctx, identifier); err == nil {
		return p, true
	} else if !errors.Is(err, ErrPlayerNotFound) {
		return nil, false
	}
	p, err := players.GetPlayerByEmail(ctx, strings.ToLower(identifier))
	if err != nil {
		return nil, false
	}

	return p, true
}
