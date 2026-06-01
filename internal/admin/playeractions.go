package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/mailer"
)

// HandlePlayerMarkVerified handles POST /admin/players/{playerID}/verify.
// Only flips when the row is currently unverified; any other state is a
// 400 so a stale browser tab does not silently re-stamp the
// already-verified timestamp.
func HandlePlayerMarkVerified(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
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
		if detail.OnboardingState != auth.OnboardingStateUnverified {
			flash.SetError(w, "This player is not in the 'unverified' state.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if err := store.SetPlayerEmailVerifiedNow(r.Context(), playerID); err != nil {
			logger.ErrorContext(r.Context(), "error marking player verified", slog.Any("err", err))
			flash.SetError(w, "Could not mark player verified. Try again.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionVerify, nil)
		flash.SetNotice(w, "Player marked verified.")
		redirectToPlayerDetail(w, r, playerID)
	})
}

// HandlePlayerResendVerification handles
// POST /admin/players/{playerID}/resend-verification. Per-target
// rate-limited (one resend per minute per playerID) so a stuck operator
// hitting the button does not turn the admin page into a mail floodgate.
// The send itself runs on a detached goroutine so the response is not
// held open while SMTP dials.
func HandlePlayerResendVerification(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	tokens auth.VerifyTokenStore,
	sender auth.VerifyEmailSender,
	baseURL string,
	limiter *PerTargetLimiter,
	flash *auth.SignedFlash,
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
		if detail.OnboardingState != auth.OnboardingStateUnverified || detail.Email == "" {
			flash.SetError(w, "Resend is only available for unverified players with an email on file.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if wait, allowed := limiter.Allow(playerID); !allowed {
			seconds := max(int((wait+time.Second-1)/time.Second), 1)
			flash.SetError(w, "Slow down: wait a moment before resending.", seconds)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		if !dispatchAdminResendVerification(
			r.Context(), logger, tokens, sender, baseURL, detail.Email, playerID,
		) {
			flash.SetError(w, "Email sending is not configured; no verification email was sent.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionResendVerification, nil)
		flash.SetNotice(w, "Verification email dispatched.")
		redirectToPlayerDetail(w, r, playerID)
	})
}

// HandlePlayerSetEmail handles POST /admin/players/{playerID}/email.
// Validates with auth.LooksLikeEmail, rejects collisions with the
// existing ErrEmailTaken sentinel, and clears email_verified_at so the
// changed address starts unverified - the operator then marks-verified
// separately or triggers a resend (per #450 spec).
func HandlePlayerSetEmail(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
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

		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		if !auth.LooksLikeEmail(email) {
			flash.SetError(w, "Enter a valid email address.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		err := store.SetPlayerEmail(r.Context(), playerID, email)
		switch {
		case err == nil:
			writeAudit(r.Context(), logger, store, actor.ID, playerID,
				auth.AdminActionEmailSet, map[string]string{"new_email": email})
			flash.SetNotice(w, "Email updated. The player still needs to verify the new address.")
		case errors.Is(err, auth.ErrEmailTaken):
			flash.SetError(w, "Another account already uses that email.", 0)
		case errors.Is(err, auth.ErrPlayerNotFound):
			flash.SetError(w, "Player not found.", 0)
		default:
			logger.ErrorContext(r.Context(), "error setting player email", slog.Any("err", err))
			flash.SetError(w, "Could not update email. Try again.", 0)
		}
		redirectToPlayerDetail(w, r, playerID)
	})
}

// HandlePlayerSetUsername handles POST /admin/players/{playerID}/username.
// Admin only (gated by RequireAdmin at the route). Renames the target row to
// the supplied display name; the store enforces the empty / taken sentinels so
// the handler only maps them to flashes.
func HandlePlayerSetUsername(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
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

		name := strings.TrimSpace(r.PostFormValue("display_name"))

		_, err := store.RenamePlayer(r.Context(), playerID, name)
		switch {
		case err == nil:
			writeAudit(r.Context(), logger, store, actor.ID, playerID,
				auth.AdminActionUsernameSet, map[string]string{"new_username": name})
			flash.SetNotice(w, "Display name updated.")
		case errors.Is(err, auth.ErrUsernameEmpty):
			flash.SetError(w, "Enter a display name.", 0)
		case errors.Is(err, auth.ErrUsernameTaken):
			flash.SetError(w, "That display name is already taken.", 0)
		case errors.Is(err, auth.ErrPlayerNotFound):
			flash.SetError(w, "Player not found.", 0)
		default:
			logger.ErrorContext(r.Context(), "error setting player username", slog.Any("err", err))
			flash.SetError(w, "Could not update display name. Try again.", 0)
		}
		redirectToPlayerDetail(w, r, playerID)
	})
}

// HandlePlayerSetPassword handles POST /admin/players/{playerID}/password.
// Admin only (gated by RequireAdmin at the route). Rotates the target's
// password and bumps session_version, signing the target out of their other
// sessions. The raw password is never logged or echoed.
func HandlePlayerSetPassword(
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
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

		password := r.PostFormValue("password")
		if len(password) < auth.MinPasswordLength {
			flash.SetError(w, fmt.Sprintf("Password must be at least %d characters.", auth.MinPasswordLength), 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}
		if len(password) > auth.MaxPasswordLength {
			flash.SetError(w, fmt.Sprintf("Password must be at most %d characters.", auth.MaxPasswordLength), 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		hash, err := auth.HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing player password", slog.Any("err", err))
			flash.SetError(w, "Could not set password. Try again.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		err = store.ChangePlayerPassword(r.Context(), playerID, hash)
		switch {
		case err == nil:
			writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionPasswordSet, nil)
			flash.SetNotice(w,
				"Password set. The player's other sessions were signed out; hand the new password over out-of-band.")
		case errors.Is(err, auth.ErrPlayerNotFound):
			flash.SetError(w, "Player not found.", 0)
		default:
			logger.ErrorContext(r.Context(), "error setting player password", slog.Any("err", err))
			flash.SetError(w, "Could not set password. Try again.", 0)
		}
		redirectToPlayerDetail(w, r, playerID)
	})
}

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
	flash *auth.SignedFlash,
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
		if removingAdmin && !guardLastAdmin(w, r, logger, store, flash, playerID) {
			return
		}

		if err := store.SetPlayerRole(r.Context(), playerID, desired); err != nil {
			if errors.Is(err, auth.ErrPlayerNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error setting player role", slog.Any("err", err))
			flash.SetError(w, "Could not update role. Try again.", 0)
			redirectToPlayerDetail(w, r, playerID)

			return
		}

		writeAudit(r.Context(), logger, store, actor.ID, playerID, auth.AdminActionRoleChanged,
			map[string]string{"from": from, "to": desired})
		flash.SetNotice(w, roleChangeNotice(desired))
		redirectToPlayerDetail(w, r, playerID)
	})
}

// guardLastAdmin refuses a change that strips Admin from the only remaining
// Admin. Called only when the change actually removes Admin; returns false
// (after flashing + 303) when the change must be blocked, true when it may
// proceed.
func guardLastAdmin(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	store auth.AdminPlayerStore, flash *auth.SignedFlash, playerID int64,
) bool {
	count, err := store.CountAdmins(r.Context())
	if err != nil {
		logger.ErrorContext(r.Context(), "error counting admins", slog.Any("err", err))
		flash.SetError(w, "Could not update role. Try again.", 0)
		redirectToPlayerDetail(w, r, playerID)

		return false
	}
	if count <= 1 {
		flash.SetError(w, "Cannot remove the last admin - promote another first.", 0)
		redirectToPlayerDetail(w, r, playerID)

		return false
	}

	return true
}

// roleChangeNotice is the success flash naming the new tier.
func roleChangeNotice(role string) string {
	switch role {
	case auth.RoleAdmin:
		return "Player role set to admin."
	case auth.RoleHost:
		return "Player role set to host."
	default:
		return "Player role set to player."
	}
}

// playerCreatePageData backs the playernew.gohtml template.
type playerCreatePageData struct {
	Title       string
	DisplayName string
	Email       string
	Error       string
}

// HandlePlayerCreateForm renders GET /admin/players/new.
func HandlePlayerCreateForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playernew.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.Render(w, r, http.StatusOK, playerCreatePageData{
			Title: "Admin Dashboard - New Player",
		})
	})
}

// HandlePlayerCreateSubmit handles POST /admin/players. Creates the row
// with email_verified_at stamped so the new account can log in
// immediately; the admin hands the credentials over out-of-band. Empty
// password is rejected because a player who cannot log in is not what
// this action is for.
func HandlePlayerCreateSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playernew.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "create-player form parse failed", slog.Any("err", err))
			render.Render(w, r, http.StatusBadRequest, playerCreatePageData{
				Title: "Admin Dashboard - New Player",
				Error: "Form was malformed or too large.",
			})

			return
		}

		input := newPlayerInput(r)
		if input.errMsg != "" {
			render.Render(w, r, http.StatusBadRequest, playerCreatePageData{
				Title:       "Admin Dashboard - New Player",
				DisplayName: input.DisplayName, Email: input.Email,
				Error: input.errMsg,
			})

			return
		}

		hash, err := auth.HashPassword(input.Password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password for admin-create", slog.Any("err", err))
			render.Render(w, r, http.StatusInternalServerError, playerCreatePageData{
				Title:       "Admin Dashboard - New Player",
				DisplayName: input.DisplayName, Email: input.Email,
				Error: "Could not create player. Try again.",
			})

			return
		}

		player, err := store.CreatePlayerByAdmin(r.Context(), input.DisplayName, input.Email, hash)
		if err != nil {
			renderCreatePlayerError(w, r, render, input, err)

			return
		}

		writeAudit(r.Context(), logger, store, actor.ID, player.ID, auth.AdminActionCreated,
			map[string]string{"new_email": input.Email})
		flash.SetNotice(w, fmt.Sprintf(
			"Player %q created with email %s. Hand the password over out-of-band; it is not stored here.",
			player.DisplayName, input.Email,
		))
		redirectToPlayerDetail(w, r, player.ID)
	})
}

// newPlayerCreateInput is the trimmed + validated form data for the
// admin-create-player flow. errMsg carries the first validation
// failure; the handler re-renders the form with that banner.
type newPlayerCreateInput struct {
	DisplayName string
	Email       string
	Password    string
	errMsg      string
}

func newPlayerInput(r *http.Request) newPlayerCreateInput {
	in := newPlayerCreateInput{
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		Email:       strings.ToLower(strings.TrimSpace(r.PostFormValue("email"))),
		Password:    r.PostFormValue("password"),
	}
	if in.DisplayName == "" {
		in.DisplayName = auth.GeneratePetname()
	}
	if !auth.LooksLikeEmail(in.Email) {
		in.errMsg = "Enter a valid email address."

		return in
	}
	if len(in.Password) < auth.MinPasswordLength {
		in.errMsg = fmt.Sprintf("Password must be at least %d characters.", auth.MinPasswordLength)

		return in
	}
	if len(in.Password) > auth.MaxPasswordLength {
		in.errMsg = fmt.Sprintf("Password must be at most %d characters.", auth.MaxPasswordLength)
	}

	return in
}

// renderCreatePlayerError maps the store-level conflict sentinels onto
// the form's re-render path. Anything else is a 500 from the
// renderer's perspective; the caller logs the underlying err
// separately for ops.
func renderCreatePlayerError(
	w http.ResponseWriter, r *http.Request, render *TemplateRenderer, in newPlayerCreateInput, err error,
) {
	status := http.StatusInternalServerError
	msg := "Could not create player. Try again."
	switch {
	case errors.Is(err, auth.ErrUsernameTaken):
		status = http.StatusConflict
		msg = "That display name is already taken."
	case errors.Is(err, auth.ErrEmailTaken):
		status = http.StatusConflict
		msg = "Another account already uses that email."
	default:
		// Fall through to the generic 500 message; err is logged by the caller.
	}
	render.Render(w, r, status, playerCreatePageData{
		Title:       "Admin Dashboard - New Player",
		DisplayName: in.DisplayName, Email: in.Email,
		Error: msg,
	})
}

// requireAdminActor returns the signed-in admin from the request
// context, or surfaces a 500 + false when the context is missing the
// player. The auth.RequireAdmin middleware guarantees the player is
// present in production; this guard is a defence-in-depth fallback so
// a misconfigured wiring layer cannot quietly write a NULL actor_id
// into admin_audit.
func requireAdminActor(w http.ResponseWriter, r *http.Request) (*auth.Player, bool) {
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		http.Error(w, "missing actor", http.StatusInternalServerError)

		return nil, false
	}

	return p, true
}

// loadActionTarget fetches the target player's detail row. A missing
// target yields a 404 + false; any other store error is a 500.
func loadActionTarget(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	store auth.AdminPlayerStore, playerID int64,
) (*auth.PlayerDetail, bool) {
	detail, err := store.GetPlayerDetail(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			http.NotFound(w, r)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error loading action target", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	return detail, true
}

// dispatchAdminResendVerification mints + dispatches the verification
// email on a detached goroutine. Mirrors auth.dispatchVerifyEmail so a
// closed browser tab does not cancel the send and SMTP latency is not
// observable from the redirect timing. Returns false without dispatching
// when email is not configured (nil tokens/sender or empty baseURL) so
// the caller can skip the audit row and the success notice.
func dispatchAdminResendVerification(
	ctx context.Context,
	logger *slog.Logger,
	tokens auth.VerifyTokenStore,
	sender auth.VerifyEmailSender,
	baseURL, recipient string,
	playerID int64,
) bool {
	if tokens == nil || sender == nil {
		return false
	}
	if baseURL == "" {
		logger.WarnContext(ctx, "admin resend verification skipped: BASE_URL is empty",
			slog.Int64("player_id", playerID))

		return false
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailer.SendTimeout+15*time.Second)
	go func() {
		defer cancel()
		auth.SendVerifyEmailBestEffort(bg, logger, tokens, sender,
			baseURL, recipient, playerID, time.Now().UTC())
	}()

	return true
}

// PerTargetLimiter is a per-player-id cool-down for admin actions that
// dispatch outbound mail. Concurrency-safe; the map is pruned of stale
// entries every Allow call so memory stays proportional to the live
// caller set. Same shape as auth.VerifyResendLimiter but keyed on
// playerID instead of source IP.
type PerTargetLimiter struct {
	mu     sync.Mutex
	last   map[int64]time.Time
	window time.Duration
	now    func() time.Time
}

// NewPerTargetLimiter returns a limiter using the supplied window and
// [time.Now] as the clock. The clock is injectable via the export_test
// seam so tests can fast-forward without sleeping.
func NewPerTargetLimiter(window time.Duration) *PerTargetLimiter {
	return newPerTargetLimiterWithClock(window, time.Now)
}

func newPerTargetLimiterWithClock(window time.Duration, now func() time.Time) *PerTargetLimiter {
	return &PerTargetLimiter{
		last:   map[int64]time.Time{},
		window: window,
		now:    now,
	}
}

// Allow reports whether target may dispatch right now. On admit stamps
// the bucket so the next call within the window is blocked; on block
// returns the remaining wait so the caller can render it.
func (l *PerTargetLimiter) Allow(target int64) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-2 * l.window)
	for k, ts := range l.last {
		if ts.Before(cutoff) {
			delete(l.last, k)
		}
	}
	if prev, ok := l.last[target]; ok {
		if remaining := l.window - now.Sub(prev); remaining > 0 {
			return remaining, false
		}
	}
	l.last[target] = now

	return 0, true
}
