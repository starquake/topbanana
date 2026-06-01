package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
)

// invitePageData backs the accept_invite.gohtml template. DisplayName is
// preserved across a failed submit so the recipient does not retype it;
// Token rides a hidden field so the POST does not need it on the URL bar.
type invitePageData struct {
	Title       string
	Token       string
	DisplayName string
	Message     string
}

// HandleAcceptInviteForm renders GET /accept-invite?token=... The
// pick-username + password form is only shown when the token decodes
// against a live (pending, unexpired) invite; otherwise it short-circuits
// to an "invite link is no longer valid" page (410) so the recipient is
// not asked to fill in a form the POST will reject.
func HandleAcceptInviteForm(logger *slog.Logger, csrfMgr *csrf.Manager, invites InviteStore) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the raw token from any cross-origin Referer the browser
		// might leak (Google Fonts on base.gohtml is the notable case).
		w.Header().Set("Referrer-Policy", "no-referrer")
		raw := r.URL.Query().Get("token")
		if !inviteLivePreflight(r, logger, invites, raw) {
			invalid.render(w, r, http.StatusGone, invitePageData{Title: "Invite"})

			return
		}
		render.render(w, r, http.StatusOK, invitePageData{Title: "Accept your invite", Token: raw})
	})
}

// inviteLivePreflight is a read-only peek so GET /accept-invite can
// short-circuit dead links without burning the single-use semantic.
// Returns true when the token matches a pending, unexpired invite. The
// atomic consume on POST is the security boundary; this peek only gates
// the render path. A lookup error other than ErrInviteInvalid (a DB
// hiccup) falls open - the POST handler re-validates, so falling open
// here only costs the recipient a second click, while falling closed
// would block a legitimate accept on a transient glitch.
func inviteLivePreflight(r *http.Request, logger *slog.Logger, invites InviteStore, raw string) bool {
	if raw == "" {
		return false
	}
	_, err := invites.GetLiveInvite(r.Context(), HashInviteToken(raw))
	if err == nil {
		return true
	}
	if errors.Is(err, ErrInviteInvalid) {
		return false
	}
	logger.WarnContext(r.Context(), "invite preflight lookup failed", slog.Any("err", err))

	return true
}

// InvitePlayerStore is the slice of player persistence the accept-invite
// flow needs: create the row, stamp it verified, and re-read it for the
// post-create session mint. Kept narrow (rather than reusing the full
// PlayerStore) so adding the verify-stamp method here does not force
// every PlayerStore stub in the codebase to grow a method it never calls.
type InvitePlayerStore interface {
	// CreatePlayer creates a credentialled player. Returns ErrUsernameTaken
	// / ErrEmailTaken on UNIQUE collisions.
	CreatePlayer(ctx context.Context, username, email, passwordHash, requestedRole string) (*Player, error)
	// MarkPlayerEmailVerifiedIfNew stamps email_verified_at when currently
	// NULL. Idempotent. Clicking the invite link proves control of the
	// invited address, so the new account lands verified.
	MarkPlayerEmailVerifiedIfNew(ctx context.Context, playerID int64) error
	// GetPlayerByID re-reads the row after create so the session is minted
	// with the persisted id + session_version. Returns ErrPlayerNotFound
	// when no row matches.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
}

// AcceptInviteDeps bundles HandleAcceptInviteSubmit's dependencies so the
// constructor stays under revive's argument cap.
type AcceptInviteDeps struct {
	Invites  InviteStore
	Players  InvitePlayerStore
	Sessions *session.Manager
}

// HandleAcceptInviteSubmit handles POST /accept-invite. It re-validates
// the token live, validates the chosen username + password (same length
// rules as register/reset), creates the player with the email carried by
// the invite and stamps it email-verified (clicking the link proves
// control of the address), consumes the invite, and auto-logs the new
// player in.
//
// Ordering avoids burning the token on a recoverable failure: the player
// is created FIRST, then the invite is consumed. A username collision
// fails the create and leaves the invite pending, so the recipient can
// retry with a different name on the same link. The invite is consumed
// only after the player row exists; a consume failure after a successful
// create is logged but the player is still signed in (the account is
// real - re-burning the link is the lesser evil than locking the new
// player out).
func HandleAcceptInviteSubmit(logger *slog.Logger, csrfMgr *csrf.Manager, deps AcceptInviteDeps) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "accept-invite form parse failed", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		raw := r.PostFormValue("token")
		invite, err := deps.Invites.GetLiveInvite(r.Context(), HashInviteToken(raw))
		if err != nil {
			invalid.render(w, r, http.StatusGone, invitePageData{Title: "Invite"})

			return
		}

		username := r.PostFormValue("display_name")
		password := r.PostFormValue("password")
		confirm := r.PostFormValue("confirm")
		if msg, ok := validateAcceptInviteInput(username, password, confirm); !ok {
			render.render(w, r, http.StatusBadRequest, invitePageData{
				Title: "Accept your invite", Token: raw, DisplayName: username, Message: msg,
			})

			return
		}

		acceptInvite(w, r, logger, render, deps, acceptInviteForm{
			invite: invite, token: raw, username: username, password: password,
		})
	})
}

// acceptInviteForm carries the validated form inputs (plus the resolved
// invite) into acceptInvite so that helper stays under revive's
// argument-limit. The email is taken from invite, not the form, so the
// address the admin vetted is the one the account carries.
type acceptInviteForm struct {
	invite   *LiveInvite
	token    string
	username string
	password string
}

// acceptInvite runs the create-player -> consume-invite -> auto-login
// sequence after validation. Split out of the handler so the closure
// stays under revive's function-length limit.
func acceptInvite(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	render *templateRenderer,
	deps AcceptInviteDeps,
	form acceptInviteForm,
) {
	hashed, err := HashPassword(form.password)
	if err != nil {
		logger.ErrorContext(r.Context(), "accept-invite hash failed", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	player, err := deps.Players.CreatePlayer(r.Context(), form.username, form.invite.Email, hashed, RolePlayer)
	if err != nil {
		if msg, ok := acceptInviteCollisionMessage(err); ok {
			render.render(w, r, http.StatusConflict, invitePageData{
				Title: "Accept your invite", Token: form.token, DisplayName: form.username, Message: msg,
			})

			return
		}
		logger.ErrorContext(r.Context(), "accept-invite create player failed", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	if markErr := deps.Players.MarkPlayerEmailVerifiedIfNew(r.Context(), player.ID); markErr != nil {
		logger.ErrorContext(r.Context(), "accept-invite mark verified failed",
			slog.Int64("player_id", player.ID), slog.Any("err", markErr))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	if consumeErr := deps.Invites.ConsumeInvite(r.Context(), HashInviteToken(form.token)); consumeErr != nil {
		// The player row already exists and is verified; signing them in is
		// the right outcome even though the invite consume lost a race. Log
		// it so a double-accept is visible, then fall through to login.
		logger.WarnContext(r.Context(), "accept-invite consume failed after create",
			slog.Int64("player_id", player.ID), slog.Any("err", consumeErr))
	}

	refreshed, err := deps.Players.GetPlayerByID(r.Context(), player.ID)
	if err != nil {
		logger.ErrorContext(r.Context(), "accept-invite post-create lookup failed",
			slog.Int64("player_id", player.ID), slog.Any("err", err))
		http.Redirect(w, r, "/login", http.StatusSeeOther)

		return
	}
	deps.Sessions.Set(w, refreshed.ID, refreshed.SessionVersion)
	http.Redirect(w, r, landingPathFor(refreshed.Role), http.StatusSeeOther)
}

// validateAcceptInviteInput pins a non-empty username plus the same
// password length + confirm-match rules the register/reset forms use.
// Returns the user-facing banner text and false when rejected.
func validateAcceptInviteInput(username, password, confirm string) (string, bool) {
	if username == "" {
		return "Pick a display name.", false
	}
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

// acceptInviteCollisionMessage maps the create-time conflict sentinels
// onto the recipient-facing banner. A taken username re-renders the form
// (the invite stays pending, so a retry works). A taken email means an
// account was created for this address between invite-send and accept (a
// race); the recipient is told to sign in. ok=false for any other error
// so the caller treats it as a 500.
func acceptInviteCollisionMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrUsernameTaken):
		return "That display name is already taken. Pick another.", true
	case errors.Is(err, ErrEmailTaken):
		return "An account already exists for this email - sign in instead.", true
	}

	return "", false
}
