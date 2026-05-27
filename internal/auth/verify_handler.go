package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
)

// verifyEmailPageData is the payload the verify-email page renders.
// ShowContinue gates the "Continue" CTA: the success and already-used
// branches show it (pointing at the role landing), the invalid-token
// branch does not.
type verifyEmailPageData struct {
	Title        string
	Heading      string
	Message      string
	ShowContinue bool
	ContinueHref string
}

// HandleVerifyEmail returns the handler for GET /verify-email?token=...
// It atomically consumes the token, stamps email_verified_at on the
// owning player, and renders a short success / already-verified /
// invalid page. The handler does NOT require an authenticated session:
// the link arrives in an inbox the user already controls, and email
// clients prefetching the link cannot keep the user from completing
// verification in a fresh browser window.
func HandleVerifyEmail(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	tokens VerifyTokenStore,
	players PlayerStore,
	sessions *session.Manager,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/verify_email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the raw token from any cross-origin Referer the browser
		// would otherwise send (Google Fonts on base.gohtml is the
		// notable case). Modern defaults strip the query already; the
		// explicit no-referrer header pins the behaviour on older UAs.
		w.Header().Set("Referrer-Policy", "no-referrer")
		raw := r.URL.Query().Get("token")
		if raw == "" {
			render.render(w, r, http.StatusBadRequest, verifyEmailPageData{
				Title:   "Verify email",
				Heading: "Link is missing",
				Message: "This verification link is missing its token. Use the link from the email exactly as it was sent.",
			})

			return
		}

		ownerID, err := tokens.ConsumeVerifyToken(r.Context(), HashVerifyToken(raw))
		landing := postVerifyLanding(w, r, players, sessions, ownerID)
		switch {
		case err == nil:
			render.render(w, r, http.StatusOK, verifyEmailPageData{
				Title:        "Email verified",
				Heading:      "Email verified",
				Message:      "Your email address is confirmed. You can now use everything Top Banana has to offer.",
				ShowContinue: true,
				ContinueHref: landing,
			})
		case errors.Is(err, ErrVerifyTokenAlreadyUsed):
			// Read the same as the first-time success: a duplicate
			// click (mail-client prefetch, browser reload) should not
			// look like an error.
			render.render(w, r, http.StatusOK, verifyEmailPageData{
				Title:        "Email verified",
				Heading:      "Already verified",
				Message:      "This email address was already verified. You can carry on.",
				ShowContinue: true,
				ContinueHref: landing,
			})
		case errors.Is(err, ErrVerifyTokenInvalid):
			render.render(w, r, http.StatusGone, verifyEmailPageData{
				Title:   "Verify email",
				Heading: "Link is no longer valid",
				Message: "This verification link has expired or was never issued. Sign in to request a fresh one.",
			})
		default:
			logger.ErrorContext(r.Context(), "verify email consume failed", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

// postVerifyLanding picks the Continue link target. Prefers the
// session player's role landing when the session belongs to the token
// owner; falls back to the neutral home page when the session is
// missing, unreadable, or belongs to a different player than the one
// the token verified. The session is cleared in the mismatch case so
// the success page does not leave the operator signed in as someone
// else after clicking another user's link on a shared device. A zero
// ownerID (consume failed) skips the mismatch check so the invalid /
// expired branch still respects an existing session.
func postVerifyLanding(
	w http.ResponseWriter,
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	ownerID int64,
) string {
	id, ok := sessions.PlayerID(r)
	if !ok {
		return playerLandingPath
	}
	if ownerID != 0 && ownerID != id {
		sessions.Clear(w)

		return playerLandingPath
	}
	p, err := players.GetPlayerByID(r.Context(), id)
	if err != nil {
		return playerLandingPath
	}

	return landingPathFor(p.Role)
}
