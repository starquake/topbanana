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
//
// The success branch covers both the register-time verify (the
// historical case) and the in-session email-change consume (#497).
// The store layer chooses which side effect runs based on the token
// row's pending_email column; this handler only sees a single
// success / already-used / invalid signal and renders the same
// confirmation either way. The store-level
// session_version bump on an email swap invalidates every other live
// cookie for the account; the current request's cookie is refreshed
// inline so the visitor stays signed in on this tab.
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
		renderVerifyOutcome(w, r, logger, render, verifyOutcome{
			logger:   logger,
			players:  players,
			sessions: sessions,
			landing:  landing,
			ownerID:  ownerID,
			err:      err,
		})
	})
}

// verifyOutcome groups the consume-result plumbing renderVerifyOutcome
// needs. Bundling keeps the helper under revive's argument-count cap
// without flattening the call site into a long positional list.
type verifyOutcome struct {
	logger   *slog.Logger
	players  PlayerStore
	sessions *session.Manager
	landing  string
	ownerID  int64
	err      error
}

// renderVerifyOutcome maps the consume result onto the rendered page.
// The success branch also refreshes the current cookie with the
// player's latest session_version so the email-change swap (which
// bumps the version inside the same DB transaction) does not log the
// initiating tab out of itself. Split out of HandleVerifyEmail so the
// constructor stays under revive's function-length cap.
func renderVerifyOutcome(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	render *templateRenderer,
	out verifyOutcome,
) {
	switch {
	case out.err == nil:
		refreshSessionAfterVerify(w, r, out.logger, out.players, out.sessions, out.ownerID)
		render.render(w, r, http.StatusOK, verifyEmailPageData{
			Title:        "Email verified",
			Heading:      "Email verified",
			Message:      "Your email address is confirmed. You can now use everything Top Banana! has to offer.",
			ShowContinue: true,
			ContinueHref: out.landing,
		})
	case errors.Is(out.err, ErrVerifyTokenAlreadyUsed):
		// Read the same as the first-time success: a duplicate
		// click (mail-client prefetch, browser reload) should not
		// look like an error.
		render.render(w, r, http.StatusOK, verifyEmailPageData{
			Title:        "Email verified",
			Heading:      "Already verified",
			Message:      "This email address was already verified. You can carry on.",
			ShowContinue: true,
			ContinueHref: out.landing,
		})
	case errors.Is(out.err, ErrEmailTaken):
		// The email-change branch raced another account that took the
		// new address between send and click. Render a distinct page
		// so the visitor sees why the swap did not apply.
		render.render(w, r, http.StatusConflict, verifyEmailPageData{
			Title:   "Verify email",
			Heading: "Address no longer available",
			Message: "That email is already attached to another account. Submit the change again with a different address.",
		})
	case errors.Is(out.err, ErrVerifyTokenInvalid):
		render.render(w, r, http.StatusGone, verifyEmailPageData{
			Title:   "Verify email",
			Heading: "Link is no longer valid",
			Message: "This verification link has expired or was never issued. Sign in to request a fresh one.",
		})
	case errors.Is(out.err, ErrPlayerNotFound):
		// Token's owning row disappeared between insert and consume
		// (account deleted, or the row was wiped by an operator).
		// Render the same expired-link page rather than 500ing - the
		// consume side already wrote consumed_at so the link cannot
		// be replayed.
		render.render(w, r, http.StatusGone, verifyEmailPageData{
			Title:   "Verify email",
			Heading: "Link is no longer valid",
			Message: "This verification link can no longer be applied. Sign in to request a fresh one.",
		})
	default:
		logger.ErrorContext(r.Context(), "verify email consume failed", slog.Any("err", out.err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// refreshSessionAfterVerify rewrites the session cookie for the
// current request when the session belongs to the player whose token
// just consumed. Only meaningful for the email-change variant (which
// bumps session_version inside the consume transaction); for the
// register-time variant the version is unchanged and the rewrite is
// a no-op. The mismatch / signed-out cases are already handled by
// postVerifyLanding, which clears or ignores the cookie before this
// helper runs.
//
// A lookup failure on the post-consume read leaves the stale cookie
// in place; the user will be bounced to /login on their next request
// because session_version no longer matches. Logged at WARN so an
// operator notices repeated occurrences (a hot DB hiccup or, worse,
// a row that vanished mid-flow).
func refreshSessionAfterVerify(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	players PlayerStore,
	sessions *session.Manager,
	ownerID int64,
) {
	if ownerID == 0 {
		return
	}
	id, ok := sessions.PlayerID(r)
	if !ok || id != ownerID {
		return
	}
	p, err := players.GetPlayerByID(r.Context(), ownerID)
	if err != nil {
		logger.WarnContext(r.Context(), "post-verify session refresh: player lookup failed",
			slog.Int64("player_id", ownerID), slog.Any("err", err))

		return
	}
	sessions.Set(w, p.ID, p.SessionVersion)
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
