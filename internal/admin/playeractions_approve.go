package admin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

// Approval-granted notice catalog keys (#1227).
const (
	emailApprovedSubjectKey locale.MessageID = "email.approved.subject"
	emailApprovedBodyKey    locale.MessageID = "email.approved.body"
)

// HandlePlayerApprove handles POST /admin/players/{playerID}/approve (#1227).
// Admin only (gated by RequireAdmin at the route). Approves a confirmed account
// so it can sign in under LOGIN_APPROVAL_REQUIRED, writes an admin_audit row, and
// notifies the player that their account is approved. Already-approved accounts
// are a 400-style flash so a stale tab does not re-stamp the timestamp.
func HandlePlayerApprove(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	sender auth.VerifyEmailSender,
	mailConfigured bool,
	baseURL string,
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

		detail, ok := loadActionTarget(w, r, logger, store, playerID)
		if !ok {
			return
		}
		if detail.ApprovedAt != nil {
			flash.SetError(w, "This account is already approved.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if err := store.SetPlayerApprovedNow(r.Context(), playerID); err != nil {
			logger.ErrorContext(r.Context(), "error approving player", slog.Any("err", err))
			flash.SetError(w, "Could not approve the account. Try again.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionApproved, nil)

		notice := "Account approved."
		notice += maybeNotifyApproved(r.Context(), logger, sender, mailConfigured, baseURL, detail, playerID, tasks)
		flash.SetNotice(w, notice)
		redirectToPlayerDetail(w, r, playerID)
	})
}

// maybeNotifyApproved dispatches the approval-granted email to the player when
// SMTP is configured and the account has an email; it returns the sentence to
// append to the success flash. An account without an email, or an instance
// without email configured, gets no mail and a flash saying so.
//
//nolint:revive // mailConfigured reflects whether SMTP is wired (an instance fact carried from mailer.StatusView), not a behavioural toggle the caller flips per request.
func maybeNotifyApproved(
	ctx context.Context,
	logger *slog.Logger,
	sender auth.VerifyEmailSender,
	mailConfigured bool,
	baseURL string,
	detail *auth.PlayerDetail,
	playerID int64,
	tasks *bgtasks.Tracker,
) string {
	if !mailConfigured {
		return " Email is not configured, so no notification was sent."
	}
	if detail.Email == "" {
		return " The account has no email, so no notification was sent."
	}
	dispatchApprovedNotice(ctx, logger, sender, detail.Email, baseURL, playerID, tasks)

	return " The player was emailed that their account is approved."
}

// dispatchApprovedNotice sends a best-effort notice that the player's account is
// approved and links to the login page. Mirrors dispatchRoleChangeNotice: the
// send runs detached with a bounded timeout so a closed tab does not cancel it
// and SMTP latency is not observable from the redirect; a failure is logged and
// never blocks the response. The player's own locale is not stored, so the
// notice uses English.
func dispatchApprovedNotice(
	ctx context.Context,
	logger *slog.Logger,
	sender auth.VerifyEmailSender,
	recipient, baseURL string,
	playerID int64,
	tasks *bgtasks.Tracker,
) {
	msg := mailer.Message{
		To:      recipient,
		Subject: locale.Translate(locale.LocaleEN, emailApprovedSubjectKey),
		Body: locale.TranslateWith(locale.LocaleEN, emailApprovedBodyKey,
			map[string]string{"link": baseURL + "/login"}),
		Kind: mailer.KindApprovalGranted,
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+15*time.Second)
	tasks.Go(func() {
		defer cancel()
		if err := sender.Send(bg, msg); err != nil {
			logger.WarnContext(bg, "approval-granted notice dispatch failed",
				slog.Int64("player_id", playerID), slog.Any("err", err))
		}
	})
}
