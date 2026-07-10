package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
)

// resetPageData backs the reset-password.gohtml template.
type resetPageData struct {
	Title   string
	Token   string
	Message string
}

// HandleResetForm renders GET /reset-password?token=... The form is
// only shown when the token decodes against a live row; otherwise we
// short-circuit to a "link is no longer valid" page so the user knows
// to request a new one. The form embeds the raw token as a hidden
// field so the follow-up POST does not need it on the URL bar.
func HandleResetForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	tokens ResetTokenStore,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/reset_password.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/reset_password_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := locale.Resolve(r)
		raw := r.URL.Query().Get("token")
		if !resetTokenLivePreflight(r, logger, tokens, raw) {
			invalid.Render(
				w,
				r,
				http.StatusGone,
				resetPageData{Title: locale.Translate(loc, "resetPassword.linkTitle")},
			)

			return
		}
		render.Render(
			w,
			r,
			http.StatusOK,
			resetPageData{Title: locale.Translate(loc, "resetPassword.heading"), Token: raw},
		)
	})
}

// resetTokenLivePreflight is a read-only peek at the row so we can
// short-circuit GET /reset-password for already-consumed or expired
// links without burning the single-use semantic. Returns true when
// the token row exists, is unconsumed, and is not expired. The full
// atomic consume happens on POST so a GET cannot accidentally
// validate; this peek only gates the render path. A lookup error
// (DB hiccup) is treated as "render the form": the POST handler is
// the security boundary, so falling open here only costs the user a
// second click - falling closed would lock everyone out on a transient
// store glitch.
func resetTokenLivePreflight(r *http.Request, logger *slog.Logger, tokens ResetTokenStore, raw string) bool {
	if raw == "" {
		return false
	}
	_, live, err := tokens.LookupResetToken(r.Context(), HashResetToken(raw))
	if err != nil {
		logger.WarnContext(r.Context(), "reset token preflight lookup failed", slog.Any("err", err))

		return true
	}

	return live
}

// HandleResetSubmit handles POST /reset-password. Validates the token,
// hashes the new password, then calls ConsumeResetToken which atomically
// marks the token consumed, rotates password_hash, and bumps
// session_version (so every cookie minted before the reset becomes
// invalid the moment the transaction commits). On success it logs the
// reset-token holder in with the post-rotation session_version and 303s
// to their role landing; every OTHER session stays invalidated by the
// rotation.
func HandleResetSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	tokens ResetTokenStore,
	sessions *session.Manager,
	players PlayerStore,
	loginApprovalRequired bool,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/reset_password.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/reset_password_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "reset-password form parse failed", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		loc := locale.Resolve(r)
		raw := r.PostFormValue("token")
		password := r.PostFormValue("password")
		confirm := r.PostFormValue("confirm")
		if msg, ok := validateResetInput(loc, password, confirm); !ok {
			render.Render(w, r, http.StatusBadRequest, resetPageData{
				Title: locale.Translate(loc, "resetPassword.heading"), Token: raw, Message: msg,
			})

			return
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "reset-password hash failed", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		playerID, err := tokens.ConsumeResetToken(r.Context(), HashResetToken(raw), hashed)
		switch {
		case err == nil:
			autoLoginAfterReset(w, r, logger, sessions, players, playerID, loginApprovalRequired)
		case errors.Is(err, ErrResetTokenInvalid):
			invalid.Render(
				w,
				r,
				http.StatusGone,
				resetPageData{Title: locale.Translate(loc, "resetPassword.linkTitle")},
			)
		default:
			logger.ErrorContext(r.Context(), "reset-password consume failed", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

// autoLoginAfterReset signs the reset-token holder in after a
// successful ConsumeResetToken and 303s to their role landing. It
// re-fetches the player so the session is minted with the
// post-rotation session_version - using the pre-rotation value would
// have the new cookie rejected on the very next request. The password
// change is already committed at this point, so any failure here
// (lookup or otherwise) must not surface as an error that hides the
// successful reset: we log it and fall back to clearing the session
// and redirecting to /login, where the just-set password works.
//
//nolint:revive // loginApprovalRequired carries the LOGIN_APPROVAL_REQUIRED instance policy (#1227), not a per-call behavioural toggle; the gate below reads it like the login path does.
func autoLoginAfterReset(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	sessions *session.Manager,
	players PlayerStore,
	playerID int64,
	loginApprovalRequired bool,
) {
	player, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		logger.ErrorContext(r.Context(), "reset-password auto-login lookup failed",
			slog.Int64("player_id", playerID), slog.Any("err", err))
		sessions.Clear(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)

		return
	}
	// An unapproved account must not gain a session by resetting its password
	// (#1227); admins are always approved so this never blocks an operator.
	if loginApprovalRequired && !player.IsApproved() {
		logger.InfoContext(r.Context(), "reset-password auto-login blocked: account not approved",
			slog.Int64("player_id", player.ID))
		http.Redirect(w, r, loginPendingApprovalPath, http.StatusSeeOther)

		return
	}
	sessions.Set(w, player.ID, player.SessionVersion)
	http.Redirect(w, r, landingPathFor(player.Role), http.StatusSeeOther)
}

// validateResetInput pins the same length rule the register form
// uses, plus a confirm-match check. Returns the user-facing banner
// text (localized for loc) and false when the input is rejected.
func validateResetInput(loc, password, confirm string) (string, bool) {
	if len(password) < MinPasswordLength {
		return locale.TranslateCount(loc, "validation.passwordTooShort", MinPasswordLength), false
	}
	if len(password) > MaxPasswordLength {
		return locale.TranslateCount(loc, "validation.passwordTooLong", MaxPasswordLength), false
	}
	if password != confirm {
		return locale.Translate(loc, "validation.passwordsNoMatch"), false
	}

	return "", true
}
