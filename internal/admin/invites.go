package admin

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

// invitePageData backs the inviteform.gohtml template. Notice carries the
// success banner after a send; Error carries a validation/conflict banner.
// Email is preserved across re-renders so a rejected submit does not drop
// the admin's input.
type invitePageData struct {
	Title  string
	Email  string
	Note   string
	Notice string
	Error  string
}

// HandleInviteForm renders GET /admin/invites/new: a minimal "invite a
// player" form (email + optional note). The full pending-list management
// UI is a later slice (#318).
func HandleInviteForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/inviteform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.Render(w, r, http.StatusOK, invitePageData{Title: "Admin Dashboard - Invite a player"})
	})
}

// InviteDeps bundles the dependencies HandleInviteSubmit needs so the
// constructor stays under revive's argument cap. Mirrors the
// adminPlayerDeps packaging in the routes layer.
type InviteDeps struct {
	Players auth.PlayerStore
	Invites auth.InviteStore
	Sender  auth.VerifyEmailSender
	BaseURL string
}

// HandleInviteSubmit handles POST /admin/invites. Reads email (+ optional
// note); rejects an email that already has an account ("sign in instead"),
// otherwise mints + persists an invite and dispatches the invite email,
// attributing it to the session admin. On success it re-renders the form
// with a confirmation banner. SendInviteEmail commits the invite row
// before the send, so a misconfigured-SMTP failure (ErrNotConfigured)
// still leaves an acceptable invite; that case is reported as a notice so
// the operator knows the link exists even though the mail did not go out.
func HandleInviteSubmit(logger *slog.Logger, csrfMgr *csrf.Manager, deps InviteDeps) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/inviteform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "invite form parse failed", slog.Any("err", err))
			render.Render(w, r, http.StatusBadRequest, invitePageData{
				Title: "Admin Dashboard - Invite a player",
				Error: "Form was malformed or too large.",
			})

			return
		}

		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		note := strings.TrimSpace(r.PostFormValue("note"))
		if !auth.LooksLikeEmail(email) {
			render.Render(w, r, http.StatusBadRequest, invitePageData{
				Title: "Admin Dashboard - Invite a player",
				Email: email, Note: note,
				Error: "Enter a valid email address.",
			})

			return
		}

		if msg, ok := inviteRejectExisting(r.Context(), logger, deps.Players, email); !ok {
			render.Render(w, r, http.StatusConflict, invitePageData{
				Title: "Admin Dashboard - Invite a player",
				Email: email, Note: note,
				Error: msg,
			})

			return
		}

		result := sendInvite(r.Context(), logger, deps, email, note, actor.ID)
		if !result.ok {
			render.Render(w, r, http.StatusInternalServerError, invitePageData{
				Title: "Admin Dashboard - Invite a player",
				Email: email, Note: note,
				Error: result.banner,
			})

			return
		}
		render.Render(w, r, http.StatusOK, invitePageData{
			Title:  "Admin Dashboard - Invite a player",
			Notice: result.banner,
		})
	})
}

// inviteRejectExisting reports whether an invite may proceed for email.
// Returns ("", false) with a user-facing message when an account already
// exists for the address (the recipient should sign in, not be invited).
// A lookup error other than not-found is logged and treated as "proceed"
// so a transient DB hiccup does not block legitimate invites - a true
// duplicate is caught at accept time by the UNIQUE email constraint.
func inviteRejectExisting(
	ctx context.Context, logger *slog.Logger, players auth.PlayerStore, email string,
) (string, bool) {
	_, err := players.GetPlayerByEmail(ctx, email)
	switch {
	case err == nil:
		return "An account already exists for this email - sign in instead.", false
	case errors.Is(err, auth.ErrPlayerNotFound):
		return "", true
	default:
		logger.ErrorContext(ctx, "invite existing-account lookup failed", slog.Any("err", err))

		return "", true
	}
}

// inviteSendResult is sendInvite's outcome: the banner to show, and
// whether the send itself failed (the invite row is still committed in
// that case, per SendInviteEmail's contract) so the caller can pick the
// HTTP status.
type inviteSendResult struct {
	banner string
	ok     bool
}

// sendInvite mints + persists the invite and dispatches the email
// synchronously. SendInviteEmail commits the invite row before the send,
// so an unconfigured mailer (ErrNotConfigured) still leaves an acceptable
// link behind; that case returns ok=true with a banner that says the link
// exists but the mail did not go out, rather than hiding the half-success.
// A store/link-build failure (no row written) returns ok=false. Running
// synchronously rather than detached keeps the committed row observable
// the instant the admin sees the confirmation (and keeps the flow
// trivially testable).
func sendInvite(
	ctx context.Context, logger *slog.Logger, deps InviteDeps, email, note string, invitedByID int64,
) inviteSendResult {
	err := auth.SendInviteEmail(
		ctx,
		deps.Invites,
		deps.Sender,
		deps.BaseURL,
		email,
		note,
		invitedByID,
		time.Now().UTC(),
	)
	switch {
	case err == nil:
		return inviteSendResult{banner: "Invite sent to " + email + ". The link is valid for 7 days.", ok: true}
	case errors.Is(err, mailer.ErrNotConfigured):
		logger.WarnContext(ctx, "invite created but email not sent: SMTP not configured",
			slog.String("to", email))

		return inviteSendResult{
			banner: "Invite created for " + email + ", but email is not configured so no message was sent.",
			ok:     true,
		}
	default:
		logger.ErrorContext(ctx, "invite create/send failed", slog.String("to", email), slog.Any("err", err))

		return inviteSendResult{banner: "Could not create the invite. Try again.", ok: false}
	}
}
