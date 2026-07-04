package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/render"
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
// pick-displayName + password form is only shown when the token decodes
// against a live (pending, unexpired) invite; otherwise it short-circuits
// to an "invite link is no longer valid" page (410) so the recipient is
// not asked to fill in a form the POST will reject.
func HandleAcceptInviteForm(logger *slog.Logger, csrfMgr *csrf.Manager, invites InviteStore) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := locale.Resolve(r)
		raw := r.URL.Query().Get("token")
		if !inviteLivePreflight(r, logger, invites, raw) {
			invalid.Render(w, r, http.StatusGone, invitePageData{Title: locale.Translate(loc, "acceptInvite.title")})

			return
		}
		renderer.Render(
			w,
			r,
			http.StatusOK,
			invitePageData{Title: locale.Translate(loc, "acceptInvite.heading"), Token: raw},
		)
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
	// CreatePlayer creates a credentialled player. Returns ErrDisplayNameTaken
	// / ErrEmailTaken on UNIQUE collisions.
	CreatePlayer(ctx context.Context, displayName, email, passwordHash, requestedRole string) (*Player, error)
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
	// Games carries an anonymous visitor's game history onto the new
	// account when they accept an invite from a guest session, matching
	// the login / Google paths (#617). Nil disables the migration.
	Games AnonymousGameMigrator
}

// HandleAcceptInviteSubmit handles POST /accept-invite. It re-validates
// the token live, validates the chosen displayName + password (same length
// rules as register/reset), creates the player with the email carried by
// the invite and stamps it email-verified (clicking the link proves
// control of the address), consumes the invite, and auto-logs the new
// player in.
//
// Ordering avoids burning the token on a recoverable failure: the player
// is created FIRST, then the invite is consumed. A displayName collision
// fails the create and leaves the invite pending, so the recipient can
// retry with a different name on the same link. The invite is consumed
// only after the player row exists; a consume failure after a successful
// create is logged but the player is still signed in (the account is
// real - re-burning the link is the lesser evil than locking the new
// player out).
func HandleAcceptInviteSubmit(logger *slog.Logger, csrfMgr *csrf.Manager, deps AcceptInviteDeps) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite.gohtml")
	invalid := newTemplateRenderer(logger, csrfMgr, "auth/pages/accept_invite_invalid.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "accept-invite form parse failed", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		loc := locale.Resolve(r)
		raw := r.PostFormValue("token")
		invite, err := deps.Invites.GetLiveInvite(r.Context(), HashInviteToken(raw))
		if err != nil {
			invalid.Render(w, r, http.StatusGone, invitePageData{Title: locale.Translate(loc, "acceptInvite.title")})

			return
		}

		displayName := r.PostFormValue("display_name")
		password := r.PostFormValue("password")
		confirm := r.PostFormValue("confirm")
		if msg, ok := validateAcceptInviteInput(loc, displayName, password, confirm); !ok {
			heading := locale.Translate(loc, "acceptInvite.heading")
			renderer.Render(w, r, http.StatusBadRequest, invitePageData{
				Title: heading, Token: raw, DisplayName: displayName, Message: msg,
			})

			return
		}

		acceptInvite(w, r, logger, renderer, deps, acceptInviteForm{
			invite: invite, token: raw, displayName: displayName, password: password,
		})
	})
}

// acceptInviteForm carries the validated form inputs (plus the resolved
// invite) into acceptInvite so that helper stays under revive's
// argument-limit. The email is taken from invite, not the form, so the
// address the admin vetted is the one the account carries.
type acceptInviteForm struct {
	invite      *LiveInvite
	token       string
	displayName string
	password    string
}

// acceptInvite runs the create-player -> consume-invite -> auto-login
// sequence after validation. Split out of the handler so the closure
// stays under revive's function-length limit.
func acceptInvite(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	renderer *render.Renderer,
	deps AcceptInviteDeps,
	form acceptInviteForm,
) {
	hashed, err := HashPassword(form.password)
	if err != nil {
		logger.ErrorContext(r.Context(), "accept-invite hash failed", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	loc := locale.Resolve(r)
	player, err := deps.Players.CreatePlayer(r.Context(), form.displayName, form.invite.Email, hashed, RolePlayer)
	if err != nil {
		if msg, ok := acceptInviteCollisionMessage(loc, err); ok {
			heading := locale.Translate(loc, "acceptInvite.heading")
			renderer.Render(w, r, http.StatusConflict, invitePageData{
				Title: heading, Token: form.token, DisplayName: form.displayName, Message: msg,
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
	// Capture the prior session (a guest who played before accepting)
	// before Set overwrites the cookie, then carry their games onto the
	// new account - same reattribution the login / Google paths do (#617).
	var priorSessionPlayerID *int64
	if id, ok := deps.Sessions.PlayerID(r); ok {
		priorSessionPlayerID = &id
	}
	deps.Sessions.Set(w, refreshed.ID, refreshed.SessionVersion)
	migrateGamesAfterSignIn(r.Context(), logger, deps.Players, deps.Games, priorSessionPlayerID, refreshed.ID)
	http.Redirect(w, r, landingPathFor(refreshed.Role), http.StatusSeeOther)
}

// validateAcceptInviteInput pins a non-empty displayName plus the same
// password length + confirm-match rules the register/reset forms use.
// Returns the user-facing banner text (localized for loc) and false when
// rejected.
func validateAcceptInviteInput(loc, displayName, password, confirm string) (string, bool) {
	if displayName == "" {
		return locale.Translate(loc, "validation.displayNameRequired"), false
	}
	if len(password) < MinPasswordLength {
		return localizeCount(loc, "validation.passwordTooShort", MinPasswordLength), false
	}
	if len(password) > MaxPasswordLength {
		return localizeCount(loc, "validation.passwordTooLong", MaxPasswordLength), false
	}
	if password != confirm {
		return locale.Translate(loc, "validation.passwordsNoMatch"), false
	}

	return "", true
}

// acceptInviteCollisionMessage maps the create-time conflict sentinels
// onto the recipient-facing banner. A taken displayName re-renders the form
// (the invite stays pending, so a retry works). A taken email means an
// account was created for this address between invite-send and accept (a
// race); the recipient is told to sign in. ok=false for any other error
// so the caller treats it as a 500.
func acceptInviteCollisionMessage(loc string, err error) (string, bool) {
	switch {
	case errors.Is(err, ErrDisplayNameTaken):
		return locale.Translate(loc, "acceptInvite.displayNameTaken"), true
	case errors.Is(err, ErrEmailTaken):
		return locale.Translate(loc, "acceptInvite.emailTaken"), true
	}

	return "", false
}
