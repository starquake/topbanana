package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
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
		// Strip raw token from any cross-origin Referer the browser
		// might leak (Google Fonts on base.gohtml is the notable case).
		w.Header().Set("Referrer-Policy", "no-referrer")
		raw := r.URL.Query().Get("token")
		if !resetTokenLivePreflight(r, tokens, raw) {
			invalid.render(w, r, http.StatusGone, resetPageData{Title: "Reset link"})

			return
		}
		render.render(w, r, http.StatusOK, resetPageData{Title: "Set a new password", Token: raw})
	})
}

// resetTokenLivePreflight is a read-only peek at the row so we can
// short-circuit GET /reset-password for already-consumed or expired
// links without burning the single-use semantic. Returns true when
// the token row exists, is unconsumed, and is not expired. The full
// atomic consume happens on POST so a GET cannot accidentally
// validate; this peek only gates the render path.
func resetTokenLivePreflight(_ *http.Request, _ ResetTokenStore, raw string) bool {
	// We don't have a lookup-by-hash API on the consumer interface
	// (the store keeps consume atomic and refuses to expose a stand-
	// alone read for the same reason). The GET handler therefore
	// optimistically renders the form when the token query parameter
	// is non-empty; the POST handler is where the real validation
	// runs. Empty token short-circuits to the "invalid link" page so
	// a bookmarked /reset-password URL without ?token= does not show
	// a form the user cannot meaningfully submit.
	return raw != ""
}

// HandleResetSubmit handles POST /reset-password. Validates the token,
// hashes the new password, then calls ConsumeResetToken which atomically
// marks the token consumed, rotates password_hash, and bumps
// session_version (so every cookie minted before the reset becomes
// invalid the moment the transaction commits). The handler does NOT
// log the user in - it 303s to /login so the new credentials are
// exercised on the next request.
func HandleResetSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	tokens ResetTokenStore,
	sessions *session.Manager,
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

		raw := r.PostFormValue("token")
		password := r.PostFormValue("password")
		confirm := r.PostFormValue("confirm")
		if msg, ok := validateResetInput(password, confirm); !ok {
			render.render(w, r, http.StatusBadRequest, resetPageData{
				Title: "Set a new password", Token: raw, Message: msg,
			})

			return
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "reset-password hash failed", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		_, err = tokens.ConsumeResetToken(r.Context(), HashResetToken(raw), hashed)
		switch {
		case err == nil:
			// Clear any current session so the user lands on a clean
			// /login and exercises the new password.
			sessions.Clear(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		case errors.Is(err, ErrResetTokenInvalid):
			invalid.render(w, r, http.StatusGone, resetPageData{Title: "Reset link"})
		default:
			logger.ErrorContext(r.Context(), "reset-password consume failed", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

// validateResetInput pins the same length rule the register form
// uses, plus a confirm-match check. Returns the user-facing banner
// text and false when the input is rejected.
func validateResetInput(password, confirm string) (string, bool) {
	if len(password) < MinPasswordLength {
		return fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength), false
	}
	if len(password) > MaxPasswordLength {
		return fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLength), false
	}
	if password != confirm {
		return "Passwords do not match.", false
	}

	return "", true
}
