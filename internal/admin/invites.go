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
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/mailer"
)

// InviteFlashCookieName is the one-shot banner cookie for the invite
// management page. Scoped to /admin/invites so a revoke/resend PRG hop and
// the create-submit PRG hop both see it, without leaking onto the rest of
// /admin.
const (
	InviteFlashCookieName = "topbanana_admin_invite_flash"
	InviteFlashCookiePath = "/admin/invites"
)

// inviteRow is one row in the pending-invites table. The InvitedBy label
// is pre-resolved ("invited by X" or empty) so the template stays
// declarative.
type inviteRow struct {
	ID        int64
	Email     string
	InvitedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// invitePageData backs the invites.gohtml management page. Pending lists
// the open invites; Email/Note preserve the create-form input across a
// rejected submit; Notice/Error carry the PRG banner.
type invitePageData struct {
	Title   string
	Pending []inviteRow
	Email   string
	Note    string
	Notice  string
	Error   string
}

// InviteDeps bundles the dependencies the invite management handlers need
// so the constructors stay under revive's argument cap. Mirrors the
// adminPlayerDeps packaging in the routes layer.
type InviteDeps struct {
	Players auth.PlayerStore
	Invites auth.InviteStore
	Sender  auth.VerifyEmailSender
	Flash   *auth.SignedFlash
	BaseURL string
}

// HandleInvitesPage renders GET /admin/invites: the canonical invite
// management page, showing the pending-invite list and the create form on
// one screen. The one-shot flash banner (set by the create/resend/revoke
// PRG hops) is read and cleared here.
func HandleInvitesPage(logger *slog.Logger, csrfMgr *csrf.Manager, deps InviteDeps) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/invites.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := loadInvitesPage(w, r, logger, csrfMgr, deps.Invites)
		if !ok {
			return
		}
		if deps.Flash != nil {
			if fr := deps.Flash.Read(w, r); fr.OK {
				data.Notice = fr.Notice
				data.Error = fr.Err
			}
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// loadInvitesPage runs the pending-invite list query and builds the
// template data. On a store failure it writes a 500 page and returns
// ok=false so the caller can early-return.
func loadInvitesPage(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	invites auth.InviteStore,
) (invitePageData, bool) {
	pending, err := invites.ListPendingInvites(r.Context())
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing pending invites", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return invitePageData{}, false
	}

	rows := make([]inviteRow, 0, len(pending))
	for _, p := range pending {
		rows = append(rows, inviteRow{
			ID:        p.ID,
			Email:     p.Email,
			InvitedBy: p.InviterDisplayName,
			CreatedAt: p.CreatedAt,
			ExpiresAt: p.ExpiresAt,
		})
	}

	return invitePageData{
		Title:   "Admin Dashboard - Invites",
		Pending: rows,
	}, true
}

// HandleInviteRedirect serves GET /admin/invites/new as a 301 to the
// canonical /admin/invites page. Slice 1 linked the dashboard at
// /admin/invites/new; this keeps any bookmarked link working without a
// second copy of the form.
func HandleInviteRedirect() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/invites", http.StatusMovedPermanently)
	})
}

// HandleInviteSubmit handles POST /admin/invites. Reads email (+ optional
// note); rejects an email that already has an account ("sign in instead"),
// otherwise mints + persists an invite and dispatches the invite email,
// attributing it to the session admin. On success it flashes a
// confirmation and 303-redirects to GET /admin/invites (PRG) so the new
// pending invite shows up in the list. SendInviteEmail commits the invite
// row before the send, so a misconfigured-SMTP failure (ErrNotConfigured)
// still leaves an acceptable invite; that case is reported as a notice so
// the operator knows the link exists even though the mail did not go out.
func HandleInviteSubmit(logger *slog.Logger, csrfMgr *csrf.Manager, deps InviteDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "invite form parse failed", slog.Any("err", err))
			renderInviteFormError(w, r, logger, csrfMgr, deps.Invites, http.StatusBadRequest, inviteFormError{
				msg: "Form was malformed or too large.",
			})

			return
		}

		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		note := strings.TrimSpace(r.PostFormValue("note"))
		if !auth.LooksLikeEmail(email) {
			renderInviteFormError(w, r, logger, csrfMgr, deps.Invites, http.StatusBadRequest, inviteFormError{
				email: email, note: note, msg: "Enter a valid email address.",
			})

			return
		}

		if msg, allow := inviteRejectExisting(r.Context(), logger, deps.Players, email); !allow {
			renderInviteFormError(w, r, logger, csrfMgr, deps.Invites, http.StatusConflict, inviteFormError{
				email: email, note: note, msg: msg,
			})

			return
		}

		result := sendInvite(r.Context(), logger, deps, email, note, actor.ID)
		if !result.ok {
			renderInviteFormError(w, r, logger, csrfMgr, deps.Invites, http.StatusInternalServerError, inviteFormError{
				email: email, note: note, msg: result.banner,
			})

			return
		}
		setInviteNotice(deps.Flash, w, result.banner)
		http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
	})
}

// HandleInviteResend handles POST /admin/invites/{id}/resend. Mints a fresh
// token, rotates the pending invite onto it, and dispatches the email with
// the new link, killing the previously emailed one. A non-pending or
// non-existent id flashes a clear "no longer pending" message instead of a
// 500. PRG-redirects to /admin/invites either way.
func HandleInviteResend(logger *slog.Logger, deps InviteDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inviteID, ok := handlers.ParseIDFromPath(w, r, logger, "id")
		if !ok {
			return
		}
		if _, ok = requireAdminActor(w, r); !ok {
			return
		}

		err := auth.ResendInviteEmail(r.Context(), deps.Invites, deps.Sender, deps.BaseURL, inviteID, time.Now().UTC())
		switch {
		case err == nil:
			setInviteNotice(deps.Flash, w,
				"Invite resent. The new link is valid for 7 days; the old link no longer works.")
		case errors.Is(err, auth.ErrInviteNotPending):
			setInviteError(deps.Flash, w, "That invite is no longer pending.")
		case errors.Is(err, mailer.ErrNotConfigured):
			logger.WarnContext(r.Context(), "invite resent but email not sent: SMTP not configured",
				slog.Int64("invite_id", inviteID))
			setInviteNotice(
				deps.Flash,
				w,
				"Invite link rotated, but email is not configured so no message was sent. The old link no longer works.",
			)
		default:
			logger.ErrorContext(r.Context(), "invite resend failed",
				slog.Int64("invite_id", inviteID), slog.Any("err", err))
			setInviteError(deps.Flash, w, "Could not resend the invite. Try again.")
		}
		http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
	})
}

// HandleInviteRevoke handles POST /admin/invites/{id}/revoke. Marks the
// pending invite revoked so its link stops resolving. A non-pending or
// non-existent id flashes a clear message instead of a 500. PRG-redirects
// to /admin/invites either way.
func HandleInviteRevoke(logger *slog.Logger, deps InviteDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inviteID, ok := handlers.ParseIDFromPath(w, r, logger, "id")
		if !ok {
			return
		}
		if _, ok = requireAdminActor(w, r); !ok {
			return
		}

		err := deps.Invites.RevokeInvite(r.Context(), inviteID)
		switch {
		case err == nil:
			setInviteNotice(deps.Flash, w, "Invite revoked. Its link no longer works.")
		case errors.Is(err, auth.ErrInviteNotPending):
			setInviteError(deps.Flash, w, "That invite is no longer pending.")
		default:
			logger.ErrorContext(r.Context(), "invite revoke failed",
				slog.Int64("invite_id", inviteID), slog.Any("err", err))
			setInviteError(deps.Flash, w, "Could not revoke the invite. Try again.")
		}
		http.Redirect(w, r, "/admin/invites", http.StatusSeeOther)
	})
}

// inviteFormError is the repopulated create-form input plus the inline
// error banner for a rejected submit. Bundled so renderInviteFormError
// stays under revive's argument cap.
type inviteFormError struct {
	email string
	note  string
	msg   string
}

// renderInviteFormError re-renders the management page at the given status
// with the create form repopulated and an inline error banner. The pending
// list is reloaded so the page stays complete on a rejected submit; a list
// failure falls through to the 500 page.
func renderInviteFormError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	invites auth.InviteStore,
	status int,
	formErr inviteFormError,
) {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/invites.gohtml")
	data, ok := loadInvitesPage(w, r, logger, csrfMgr, invites)
	if !ok {
		return
	}
	data.Email = formErr.email
	data.Note = formErr.note
	data.Error = formErr.msg
	render.Render(w, r, status, data)
}

// setInviteNotice / setInviteError stash a one-shot banner for the next GET
// /admin/invites. A nil flash (defence-in-depth against a misconfigured
// wiring layer) is a no-op so the PRG redirect still lands.
func setInviteNotice(flash *auth.SignedFlash, w http.ResponseWriter, msg string) {
	if flash != nil {
		flash.SetNotice(w, msg)
	}
}

func setInviteError(flash *auth.SignedFlash, w http.ResponseWriter, msg string) {
	if flash != nil {
		flash.SetError(w, msg, 0)
	}
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
