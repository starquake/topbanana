package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

// Role-change notice catalog keys.
const (
	emailRoleChangeSubjectKey    locale.MessageID = "email.roleChange.subject"
	emailRoleChangeBodyKey       locale.MessageID = "email.roleChange.body"
	emailRoleChangeRolePlayerKey locale.MessageID = "email.roleChange.rolePlayer"
	emailRoleChangeRoleHostKey   locale.MessageID = "email.roleChange.roleHost"
	emailRoleChangeRoleAdminKey  locale.MessageID = "email.roleChange.roleAdmin"
)

// roleIsValid reports whether the "role" form value is one of the three
// accepted tiers (#538).
func roleIsValid(role string) bool {
	switch role {
	case auth.RolePlayer, auth.RoleHost, auth.RoleAdmin:
		return true
	default:
		return false
	}
}

// HandlePlayerSetRole handles POST /admin/players/{playerID}/role (#538).
// Admin only (gated by RequireAdmin at the route). Reads the desired tier from
// the "role" form field, diffs it against the target's current role, and
// applies the change. The last-admin guard refuses a change that would remove
// the only remaining Admin. One admin_audit row (role_changed) is written per
// real change, with the payload carrying {from, to}.
func HandlePlayerSetRole(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	sender auth.VerifyEmailSender,
	mailConfigured bool,
	flash *auth.SignedFlash,
	tasks *bgtasks.Tracker,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		playerID, ok := handlers.ParseIDFromPath(w, r, logger, "playerID")
		if !ok {
			return
		}
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}
		if !parseActionForm(w, r, logger, "set-role") {
			return
		}

		desired := r.PostFormValue("role")
		if !roleIsValid(desired) {
			flash.SetError(w, "Choose a role of player, host, or admin.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		detail, ok := loadActionTarget(w, r, logger, store, playerID)
		if !ok {
			return
		}

		from := detail.Role
		if from == desired {
			flash.SetNotice(w, "Role unchanged.")
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		removingAdmin := from == auth.RoleAdmin && desired != auth.RoleAdmin
		if removingAdmin {
			if !writeAdminDemotion(w, r, logger, store, flash, playerID, desired) {
				return
			}
		} else if !writeRoleChange(w, r, logger, store, flash, playerID, desired) {
			return
		}

		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionRoleChanged,
			map[string]string{"from": from, "to": desired})

		notice := roleChangeNotice(desired) +
			maybeNotifyRoleChange(r, logger, sender, mailConfigured, detail, desired, playerID, tasks)
		flash.SetNotice(w, notice)
		redirectToPlayerDetail(w, r, playerID)
	})
}

// writeAdminDemotion persists a demotion away from Admin via the atomic
// store guard: a missing target is flashed as "not found", the last-admin
// refusal as "promote another first", any other store error as a flashed
// retry. The count-and-update happen in one statement in the store, so two
// concurrent demotions of the two remaining admins cannot both pass (#997).
// Returns true only when the demotion was written.
func writeAdminDemotion(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	store auth.AdminPlayerStore, flash *auth.SignedFlash, playerID int64, desired string,
) bool {
	err := store.DemoteAdmin(r.Context(), playerID, desired)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrLastAdmin):
		flash.SetError(w, "Cannot remove the last admin - promote another first.", 0)
		redirectToPlayerDetail(w, r, playerID)
	case errors.Is(err, auth.ErrPlayerNotFound):
		flash.SetError(w, "Player not found.", 0)
		redirectToPlayerDetail(w, r, playerID)
	default:
		logger.ErrorContext(r.Context(), "error demoting admin", slog.Any("err", err))
		flash.SetError(w, "Could not update role. Try again.", 0)
		redirectToPlayerDetail(w, r, playerID)
	}

	return false
}

// writeRoleChange persists the role and maps a write failure onto its
// response: a missing target is a 404, any other store error a flashed
// retry + 303. Returns true only when the role was written.
func writeRoleChange(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	store auth.AdminPlayerStore, flash *auth.SignedFlash, playerID int64, desired string,
) bool {
	err := store.SetPlayerRole(r.Context(), playerID, desired)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrPlayerNotFound):
		// Match the other player-action handlers: flash + 303 back, not a bare 404.
		flash.SetError(w, "Player not found.", 0)
		redirectToPlayerDetail(w, r, playerID)
	default:
		logger.ErrorContext(r.Context(), "error setting player role", slog.Any("err", err))
		flash.SetError(w, "Could not update role. Try again.", 0)
		redirectToPlayerDetail(w, r, playerID)
	}

	return false
}

// roleLabel maps a role constant to its human word, so the success
// flash and the notification email name the tier the same way.
func roleLabel(role string) string {
	switch role {
	case auth.RoleAdmin:
		return "admin"
	case auth.RoleHost:
		return "host"
	default:
		return "player"
	}
}

// roleChangeNotice is the success flash naming the new tier.
func roleChangeNotice(role string) string {
	return "Player role set to " + roleLabel(role) + "."
}

// localizedRoleLabel maps a role constant to its word in loc, for the
// player-facing role-change email.
func localizedRoleLabel(loc, role string) string {
	switch role {
	case auth.RoleAdmin:
		return locale.Translate(loc, emailRoleChangeRoleAdminKey)
	case auth.RoleHost:
		return locale.Translate(loc, emailRoleChangeRoleHostKey)
	default:
		return locale.Translate(loc, emailRoleChangeRolePlayerKey)
	}
}

// maybeNotifyRoleChange honours the opt-in checkbox: when it is unset it
// returns an empty string so the flash is unchanged. When set, it
// dispatches the role-change notice (if SMTP is configured and the target
// has a verified email) and returns the sentence to append to the success
// flash. A player without a verified address, or an instance without
// email configured, gets no mail and a flash that says so.
//
//nolint:revive // mailConfigured reflects whether SMTP is wired (an instance fact carried from mailer.StatusView), not a behavioural toggle the caller flips per request.
func maybeNotifyRoleChange(
	r *http.Request,
	logger *slog.Logger,
	sender auth.VerifyEmailSender,
	mailConfigured bool,
	detail *auth.PlayerDetail,
	desired string,
	playerID int64,
	tasks *bgtasks.Tracker,
) string {
	if r.PostFormValue("notify_email") == "" {
		return ""
	}
	if !mailConfigured {
		return " Email is not configured, so no notification was sent."
	}
	if detail.Email == "" || detail.EmailVerifiedAt == nil {
		return " The player has no verified email, so no notification was sent."
	}
	// The player's own locale is not stored, so the notice uses the
	// acting admin's request locale.
	dispatchRoleChangeNotice(r.Context(), logger, sender, detail.Email, desired, playerID, tasks, locale.Resolve(r))

	return " A notification email was sent to the player."
}

// dispatchRoleChangeNotice sends a best-effort notice to the player that
// an administrator changed their role. Mirrors notifyOldAddressOfChange:
// the send runs on a detached goroutine with a bounded timeout so a
// closed browser tab does not cancel it and SMTP latency is not
// observable from the redirect; a failure is logged at Warn and never
// blocks the response.
func dispatchRoleChangeNotice(
	ctx context.Context,
	logger *slog.Logger,
	sender auth.VerifyEmailSender,
	recipient, role string,
	playerID int64,
	tasks *bgtasks.Tracker,
	loc string,
) {
	msg := mailer.Message{
		To:      recipient,
		Subject: locale.Translate(loc, emailRoleChangeSubjectKey),
		Body: locale.TranslateWith(loc, emailRoleChangeBodyKey,
			map[string]string{"role": localizedRoleLabel(loc, role)}),
		Kind: mailer.KindRoleChangeNotice,
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+15*time.Second)
	tasks.Go(func() {
		defer cancel()
		if err := sender.Send(bg, msg); err != nil {
			logger.WarnContext(bg, "role change notice dispatch failed",
				slog.Int64("player_id", playerID), slog.Any("err", err))
		}
	})
}
