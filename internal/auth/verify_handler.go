package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/session"
)

// Awaiting-approval notice catalog keys (#1227): the registrant notice and the
// admin fan-out. The approval-granted notice lives with the admin approve action.
const (
	emailApprovalPendingSubjectKey locale.MessageID = "email.approvalPending.subject"
	emailApprovalPendingBodyKey    locale.MessageID = "email.approvalPending.body"
	emailApprovalRequestSubjectKey locale.MessageID = "email.approvalRequest.subject"
	emailApprovalRequestBodyKey    locale.MessageID = "email.approvalRequest.body"
)

// logPlayerIDKey is the structured-log attribute key for a player id in this
// file's best-effort verify/approval warnings.
const logPlayerIDKey = "player_id"

// AdminEmailLister returns the address of every admin, backing the
// awaiting-approval fan-out (#1227). Narrow so the verify handler need not import
// internal/store.
type AdminEmailLister interface {
	// ListAdminEmails returns the email of every admin with an address on file.
	ListAdminEmails(ctx context.Context) ([]string, error)
}

// VerifyEmailDeps bundles the dependencies HandleVerifyEmail needs. Bundling
// keeps the constructor under revive's argument limit.
type VerifyEmailDeps struct {
	Tokens   VerifyTokenStore
	Players  PlayerStore
	Roles    RoleSetter
	Sessions *session.Manager
	// AdminEmails is the ADMIN_EMAILS allowlist consulted at verify time.
	AdminEmails []string
	// LoginApprovalRequired gates the awaiting-approval notices below; when off,
	// verify behaves exactly as before and the mail deps go unused (#1227).
	LoginApprovalRequired bool
	Sender                VerifyEmailSender
	AdminEmailLister      AdminEmailLister
	BaseURL               string
	// Tasks drains the detached approval notices on graceful shutdown (#740).
	// Nil in unit tests, which run untracked.
	Tasks *bgtasks.Tracker
}

// RoleSetter promotes a player to a role at email-verify time. The
// concrete store.PlayerStore satisfies it via SetPlayerRole; the narrow
// interface lives here so the verify handler can stamp the admin role
// without importing internal/store.
type RoleSetter interface {
	// SetPlayerRole sets the role on the row identified by id. Returns
	// ErrPlayerNotFound when no row matches.
	SetPlayerRole(ctx context.Context, playerID int64, role string) error
}

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
// deps.AdminEmails is the ADMIN_EMAILS allowlist; on a fresh verify the
// handler stamps the admin role when the now-proven address matches an
// entry (#785). Registration deliberately leaves the role at player so
// admin is never granted on an unverified address. When
// deps.LoginApprovalRequired is on, a fresh verify also notifies the
// registrant and every admin that the account is awaiting approval (#1227).
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
	deps VerifyEmailDeps,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/verify_email.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("token")
		if raw == "" {
			loc := locale.Resolve(r)
			renderer.Render(w, r, http.StatusBadRequest, verifyEmailPageData{
				Title:   locale.Translate(loc, "verifyEmail.title"),
				Heading: locale.Translate(loc, "verifyEmail.missingHeading"),
				Message: locale.Translate(loc, "verifyEmail.missingMessage"),
			})

			return
		}

		ownerID, err := deps.Tokens.ConsumeVerifyToken(r.Context(), HashVerifyToken(raw))
		if err == nil {
			promoted := promoteVerifiedAdminIfAllowlisted(
				r.Context(), logger, deps.Players, deps.Roles, deps.AdminEmails, ownerID)
			maybeNotifyAwaitingApproval(r.Context(), logger, deps, ownerID, promoted, locale.Resolve(r))
		}
		landing := postVerifyLanding(w, r, deps.Players, deps.Sessions, ownerID)
		renderVerifyOutcome(w, r, logger, renderer, verifyOutcome{
			logger:   logger,
			players:  deps.Players,
			sessions: deps.Sessions,
			landing:  landing,
			ownerID:  ownerID,
			err:      err,
		})
	})
}

// approvalNotifier is the mail slice the awaiting-approval fan-out needs (#1227),
// shared by the verify path and the OAuth path so both dispatch the same notices
// the same way.
type approvalNotifier struct {
	sender  VerifyEmailSender
	lister  AdminEmailLister
	baseURL string
	tasks   *bgtasks.Tracker
}

// approvalNotifier builds the mail slice from the verify deps.
func (d VerifyEmailDeps) approvalNotifier() approvalNotifier {
	return approvalNotifier{
		sender:  d.Sender,
		lister:  d.AdminEmailLister,
		baseURL: d.BaseURL,
		tasks:   d.Tasks,
	}
}

// maybeNotifyAwaitingApproval fires the awaiting-approval notices after a
// first-time verify (#1227), only when approval is required and the account is
// neither admin nor already approved. Reuses the promotion read when the caller
// passes it (player non-nil), else reads the row. Best-effort: a lookup failure
// is logged.
func maybeNotifyAwaitingApproval(
	ctx context.Context,
	logger *slog.Logger,
	deps VerifyEmailDeps,
	ownerID int64,
	player *Player,
	loc string,
) {
	if !deps.LoginApprovalRequired || ownerID == 0 {
		return
	}
	p := player
	if p == nil {
		var err error
		p, err = deps.Players.GetPlayerByID(ctx, ownerID)
		if err != nil {
			logger.WarnContext(ctx, "awaiting-approval notice: player lookup failed",
				slog.Int64(logPlayerIDKey, ownerID), slog.Any("err", err))

			return
		}
	}
	if p.IsAdmin() || p.IsApproved() {
		return
	}
	n := deps.approvalNotifier()
	dispatchApprovalPending(ctx, logger, n, p, loc)
	dispatchApprovalAdminNotice(ctx, logger, n, p)
}

// ApprovalNoticeMargin bounds each detached awaiting-approval send (on top of
// mailer.SendTimeout) so SMTP latency is never observable from the response
// that triggered it (#1227). Exported so the admin approve action shares it.
const ApprovalNoticeMargin = 15 * time.Second

// dispatchApprovalPending tells the registrant their address is confirmed and an
// admin will review the account. Detached + bounded so a closed tab does not
// cancel it; a nil sender (unit tests) skips the send.
func dispatchApprovalPending(
	ctx context.Context,
	logger *slog.Logger,
	n approvalNotifier,
	player *Player,
	loc string,
) {
	if n.sender == nil || player.Email == "" {
		return
	}
	msg := mailer.Message{
		To:      player.Email,
		Subject: locale.Translate(loc, emailApprovalPendingSubjectKey),
		Body:    locale.Translate(loc, emailApprovalPendingBodyKey),
		Kind:    mailer.KindApprovalPending,
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+ApprovalNoticeMargin)
	n.tasks.Go(func() {
		defer cancel()
		if err := n.sender.Send(bg, msg); err != nil {
			logger.WarnContext(bg, "approval-pending notice dispatch failed",
				slog.Int64(logPlayerIDKey, player.ID), slog.Any("err", err))
		}
	})
}

// dispatchApprovalAdminNotice tells every admin an account is waiting, naming the
// registrant and linking to the players list. English (admins are operators).
// The admin lookup runs inside the detached goroutine so the response is never
// held open on it; a nil sender or lister (unit tests) skips it.
func dispatchApprovalAdminNotice(
	ctx context.Context,
	logger *slog.Logger,
	n approvalNotifier,
	player *Player,
) {
	if n.sender == nil || n.lister == nil {
		return
	}
	link := n.baseURL + "/admin/players"
	name := player.DisplayName
	email := player.Email
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+ApprovalNoticeMargin)
	n.tasks.Go(func() {
		defer cancel()
		admins, err := n.lister.ListAdminEmails(bg)
		if err != nil {
			logger.WarnContext(bg, "approval-request notice: admin lookup failed", slog.Any("err", err))

			return
		}
		body := locale.TranslateWith(locale.LocaleEN, emailApprovalRequestBodyKey,
			map[string]string{"name": name, "email": email, "link": link})
		subject := locale.Translate(locale.LocaleEN, emailApprovalRequestSubjectKey)
		for _, to := range admins {
			msg := mailer.Message{To: to, Subject: subject, Body: body, Kind: mailer.KindApprovalRequest}
			if err := n.sender.Send(bg, msg); err != nil {
				logger.WarnContext(bg, "approval-request notice dispatch failed",
					slog.String("to", to), slog.Any("err", err))
			}
		}
	})
}

// promoteVerifiedAdminIfAllowlisted stamps the admin role on the player
// whose verify token just consumed, when the now-proven email matches
// the ADMIN_EMAILS allowlist (#785). The check runs against the freshly
// verified address, not the address submitted at registration, so admin
// is only ever granted on an address the user controls.
//
// Best-effort: a lookup or role-write failure is logged and the verify
// flow still renders success. The player keeps their current (player)
// role and an operator can promote them by hand or the next verify hits
// the same path. A row already at admin is left untouched so the write
// is skipped on the common already-promoted re-verify.
//
// Returns the player it read, reflecting the promotion in memory (role +
// approved_at) when one happened, so a caller can reuse it instead of reading
// the row again (#1227). Nil when there is nothing to check (no allowlist / zero
// id) or the lookup failed.
func promoteVerifiedAdminIfAllowlisted(
	ctx context.Context,
	logger *slog.Logger,
	players PlayerStore,
	roles RoleSetter,
	adminEmails []string,
	ownerID int64,
) *Player {
	if ownerID == 0 || len(adminEmails) == 0 {
		return nil
	}
	p, err := players.GetPlayerByID(ctx, ownerID)
	if err != nil {
		logger.WarnContext(ctx, "verify admin promotion: player lookup failed",
			slog.Int64(logPlayerIDKey, ownerID), slog.Any("err", err))

		return nil
	}
	if p.Role == RoleAdmin || !slices.Contains(adminEmails, p.Email) {
		return p
	}
	if err := roles.SetPlayerRole(ctx, ownerID, RoleAdmin); err != nil {
		logger.WarnContext(ctx, "verify admin promotion: set role failed",
			slog.Int64(logPlayerIDKey, ownerID), slog.Any("err", err))

		return p
	}
	// SetPlayerRole stamps approved_at when the role becomes admin (#1227), so
	// mirror both in the returned row to spare the caller a second read.
	p.Role = RoleAdmin
	if p.ApprovedAt == nil {
		now := time.Now().UTC()
		p.ApprovedAt = &now
	}

	return p
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
	renderer *render.Renderer,
	out verifyOutcome,
) {
	loc := locale.Resolve(r)
	switch {
	case out.err == nil:
		refreshSessionAfterVerify(w, r, out.logger, out.players, out.sessions, out.ownerID)
		renderer.Render(w, r, http.StatusOK, verifyEmailPageData{
			Title:        locale.Translate(loc, "verifyEmail.verifiedHeading"),
			Heading:      locale.Translate(loc, "verifyEmail.verifiedHeading"),
			Message:      locale.Translate(loc, "verifyEmail.verifiedMessage"),
			ShowContinue: true,
			ContinueHref: out.landing,
		})
	case errors.Is(out.err, ErrVerifyTokenAlreadyUsed):
		// Read the same as the first-time success: a duplicate
		// click (mail-client prefetch, browser reload) should not
		// look like an error.
		renderer.Render(w, r, http.StatusOK, verifyEmailPageData{
			Title:        locale.Translate(loc, "verifyEmail.verifiedHeading"),
			Heading:      locale.Translate(loc, "verifyEmail.alreadyHeading"),
			Message:      locale.Translate(loc, "verifyEmail.alreadyMessage"),
			ShowContinue: true,
			ContinueHref: out.landing,
		})
	case errors.Is(out.err, ErrEmailTaken):
		// The email-change branch raced another account that took the
		// new address between send and click. Render a distinct page
		// so the visitor sees why the swap did not apply.
		renderer.Render(w, r, http.StatusConflict, verifyEmailPageData{
			Title:   locale.Translate(loc, "verifyEmail.title"),
			Heading: locale.Translate(loc, "verifyEmail.takenHeading"),
			Message: locale.Translate(loc, "verifyEmail.takenMessage"),
		})
	case errors.Is(out.err, ErrVerifyTokenInvalid):
		renderer.Render(w, r, http.StatusGone, verifyEmailPageData{
			Title:   locale.Translate(loc, "verifyEmail.title"),
			Heading: locale.Translate(loc, "verifyEmail.invalidHeading"),
			Message: locale.Translate(loc, "verifyEmail.invalidMessage"),
		})
	case errors.Is(out.err, ErrPlayerNotFound):
		// Token's owning row disappeared between insert and consume
		// (account deleted, or the row was wiped by an operator).
		// Render the same expired-link page rather than 500ing - the
		// consume side already wrote consumed_at so the link cannot
		// be replayed.
		renderer.Render(w, r, http.StatusGone, verifyEmailPageData{
			Title:   locale.Translate(loc, "verifyEmail.title"),
			Heading: locale.Translate(loc, "verifyEmail.invalidHeading"),
			Message: locale.Translate(loc, "verifyEmail.notFoundMessage"),
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
			slog.Int64(logPlayerIDKey, ownerID), slog.Any("err", err))

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
