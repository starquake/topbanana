package profile

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/mailer"
)

// EmailChangeFlashCookieName / EmailChangeFlashCookiePath are the
// per-flow constants the wiring layer feeds into [auth.NewSignedFlash]
// when building the email-change flash. Exported so integration tests
// can assert on the cookie name without re-declaring it.
const (
	EmailChangeFlashCookieName = "topbanana_email_change_flash"
	EmailChangeFlashCookiePath = "/profile/email"
)

// emailDispatchTimeout caps the detached SMTP attempt the POST
// handler spawns. Matches the verify-resend / forgot-password
// timeout: above mailer.SendTimeout so the inner dial gets its own
// 30s budget before this outer timeout fires.
const emailDispatchTimeout = 45 * time.Second

// emailPageData backs profile_email.gohtml. CurrentEmail is the
// signed-in player's verified address; the form's "new" input is
// not preserved across renders because the address either succeeded
// (303 + flash) or was rejected with a banner the user can fix.
type emailPageData struct {
	Title        string
	CurrentEmail string
	Notice       string
	Message      string
	// HasPassword gates the change form: a password account must
	// re-authenticate with its current password to start a change. An
	// OAuth-only account (no password) cannot self-change its email here
	// at all (#534) - its address is attested by the sign-in provider -
	// so the template shows a "managed by your sign-in provider" notice
	// instead of the form.
	HasPassword bool
}

// HandleProfileEmail returns the [http.Handler] for GET
// /profile/email. RequireAuthenticated upstream guarantees the
// context carries a credentialled *Player; the verified-email gate
// upstream guarantees Email is non-empty and EmailVerifiedAt is set.
func HandleProfileEmail(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	flash *auth.SignedFlash,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile_email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "profile email handler reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		data := emailPageData{
			Title:        "Change email",
			CurrentEmail: player.Email,
			HasPassword:  player.PasswordHash != "",
		}
		if fr := flash.Read(w, r); fr.OK {
			data.Notice = fr.Notice
			data.Message = fr.Err
		}
		render.renderAny(w, r, http.StatusOK, data)
	})
}

// EmailChangeDeps groups the persistence + mail-side dependencies
// the POST handler needs. Bundled so the constructor signature stays
// under revive's argument-count cap.
type EmailChangeDeps struct {
	Players auth.PlayerStore
	Tokens  auth.VerifyTokenStore
	Sender  auth.VerifyEmailSender
	Flash   *auth.SignedFlash
	BaseURL string
}

// HandleProfileEmailChange returns the [http.Handler] for POST
// /profile/email. Validates the new address, refuses obvious no-ops,
// and dispatches a verify-email link tagged with the pending address
// so the consume side can swap players.email atomically when the
// link is clicked. Critically, the players row is NOT updated here:
// if the user mistyped the new address they would otherwise lock
// themselves out by orphaning the current verified mailbox before
// proving control of the new one. Account-existence opacity (the
// collision branch returns the same flash as the success branch)
// keeps this endpoint from becoming an enumeration oracle in the
// way the forgot-password and verify-resend endpoints already
// guard against.
func HandleProfileEmailChange(logger *slog.Logger, deps EmailChangeDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "profile email change reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing profile email form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		// An account with no password signs in through a provider, which
		// attests the address, so the email is not self-editable here
		// (#534). Refuse on account type before touching the input - what
		// was typed is irrelevant - which keeps the session-hijack takeover
		// surface closed without an OAuth re-auth step-up.
		if player.PasswordHash == "" {
			logger.InfoContext(r.Context(), "profile email change blocked: account has no password",
				slog.Int64("player_id", player.ID))
			deps.Flash.SetError(w, "Your email is managed by your sign-in provider and can't be changed here.", 0)
			http.Redirect(w, r, "/profile/email", http.StatusSeeOther)

			return
		}

		newEmail := strings.ToLower(strings.TrimSpace(r.PostFormValue("new_email")))
		if msg, ok := validateEmailChange(newEmail, player.Email); !ok {
			deps.Flash.SetError(w, msg, 0)
			http.Redirect(w, r, "/profile/email", http.StatusSeeOther)

			return
		}

		current := r.PostFormValue("current_password")
		if auth.CheckPassword(player.PasswordHash, current) != nil {
			logger.InfoContext(r.Context(), "profile email change rejected: current password incorrect",
				slog.Int64("player_id", player.ID))
			deps.Flash.SetError(w, "Current password is incorrect.", 0)
			http.Redirect(w, r, "/profile/email", http.StatusSeeOther)

			return
		}

		dispatchEmailChangeIfFree(r.Context(), logger, deps, player.ID, player.Email, newEmail)

		// Always flash the same notice regardless of whether the
		// address was free. The user knows what they typed, so the
		// notice can echo the address; an attacker cannot tell from
		// the response whether that address was already in use.
		deps.Flash.SetNotice(w, "We sent a verification link to "+newEmail+". Click it to switch your email.")
		http.Redirect(w, r, "/profile/email", http.StatusSeeOther)
	})
}

// validateEmailChange runs the form-level rules: non-empty, looks
// like an email, not equal to the current verified address. Returns
// the banner text and false when the input is rejected.
func validateEmailChange(newEmail, currentEmail string) (string, bool) {
	if newEmail == "" {
		return "Enter a new email address.", false
	}
	if !auth.LooksLikeEmail(newEmail) {
		return "Enter a valid email address.", false
	}
	if newEmail == strings.ToLower(strings.TrimSpace(currentEmail)) {
		return "That is already your address.", false
	}

	return "", true
}

// dispatchEmailChangeIfFree mints + sends the verify-with-pending
// email when the address is not attached to another credentialled
// account. Runs on a detached goroutine so the response timing is
// independent of the SMTP round-trip and the lookup result.
// Account-existence-opaque: any failure (collision, lookup error,
// SMTP failure) is logged but never surfaced to the user, so the
// caller can render the same flash for hit, miss, and miss-due-to-
// error.
func dispatchEmailChangeIfFree(
	ctx context.Context,
	logger *slog.Logger,
	deps EmailChangeDeps,
	playerID int64,
	oldEmail, newEmail string,
) {
	existing, err := deps.Players.GetPlayerByEmail(ctx, newEmail)
	switch {
	case err == nil:
		// Address is in use. Self-collision (changing to the email
		// already on this account) is already rejected by
		// validateEmailChange, but a race could still produce one;
		// treat it as a no-op rather than mint a swap to your own
		// address.
		if existing.ID != playerID {
			return
		}
	case errors.Is(err, auth.ErrPlayerNotFound):
		// Address is free - fall through to dispatch.
	default:
		logger.WarnContext(ctx, "profile email change lookup failed",
			slog.Int64("player_id", playerID), slog.Any("err", err))

		return
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), emailDispatchTimeout)
	go func() {
		defer cancel()
		if sendErr := auth.SendVerifyEmailWithPending(
			sendCtx, deps.Tokens, deps.Sender, deps.BaseURL,
			newEmail, newEmail, playerID, time.Now().UTC(),
		); sendErr != nil {
			logger.WarnContext(sendCtx, "profile email change dispatch failed",
				slog.Int64("player_id", playerID), slog.Any("err", sendErr))
		}
		notifyOldAddressOfChange(sendCtx, logger, deps.Sender, playerID, oldEmail, newEmail)
	}()
}

// notifyOldAddressOfChange sends a best-effort notice to the account's
// current (old) address that a change to newEmail was requested, so a
// hijacked-session change is visible to the legitimate owner. The
// notice goes to the authenticated user's own mailbox, so naming the
// new address is safe. A send failure is logged at Warn and never
// blocks the response.
func notifyOldAddressOfChange(
	ctx context.Context,
	logger *slog.Logger,
	sender auth.VerifyEmailSender,
	playerID int64,
	oldEmail, newEmail string,
) {
	msg := mailer.Message{
		To:      oldEmail,
		Subject: "Email change requested for your Top Banana! account",
		Body: "Someone requested to change the email on your Top Banana! account to " + newEmail + ".\n\n" +
			"Your account email has not changed yet; it only changes when the verification link sent to the new address is clicked.\n\n" +
			"If this was you, no action is needed. If it was not you, change your password now to secure your account.\n",
		Kind: mailer.KindEmailChangeNotice,
	}
	if err := sender.Send(ctx, msg); err != nil {
		logger.WarnContext(ctx, "profile email change notice to old address failed",
			slog.Int64("player_id", playerID), slog.Any("err", err))
	}
}
