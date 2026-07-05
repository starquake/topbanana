package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/render"
)

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
		if !parseActionForm(w, r, logger, "set-email") {
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

// HandlePlayerSetDisplayName handles POST /admin/players/{playerID}/display-name.
// Admin only (gated by RequireAdmin at the route). Renames the target row to
// the supplied display name; the store enforces the empty / taken sentinels so
// the handler only maps them to flashes.
func HandlePlayerSetDisplayName(
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
		if !parseActionForm(w, r, logger, "set-display-name") {
			return
		}

		name := strings.TrimSpace(r.PostFormValue("display_name"))

		_, err := store.AdminRenamePlayer(r.Context(), playerID, name)
		switch {
		case err == nil:
			writeAudit(r.Context(), logger, store, actor.ID, playerID,
				auth.AdminActionDisplayNameSet, map[string]string{"new_displayName": name})
			flash.SetNotice(w, "Display name updated.")
		case errors.Is(err, auth.ErrDisplayNameEmpty):
			flash.SetError(w, "Enter a display name.", 0)
		case errors.Is(err, auth.ErrDisplayNameTaken):
			flash.SetError(w, "That display name is already taken.", 0)
		case errors.Is(err, auth.ErrPlayerNotFound):
			flash.SetError(w, "Player not found.", 0)
		default:
			logger.ErrorContext(r.Context(), "error setting player displayName", slog.Any("err", err))
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
		if !parseActionForm(w, r, logger, "set-password") {
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

// playerCreatePageData backs the playernew.gohtml template.
type playerCreatePageData struct {
	Title       string
	DisplayName string
	Email       string
	Error       string
}

// HandlePlayerCreateForm renders GET /admin/players/new.
func HandlePlayerCreateForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playernew.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderer.Render(w, r, http.StatusOK, playerCreatePageData{
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
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playernew.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			logger.InfoContext(r.Context(), "create-player form parse failed", slog.Any("err", err))
			renderer.Render(w, r, http.StatusBadRequest, playerCreatePageData{
				Title: "Admin Dashboard - New Player",
				Error: "Form was malformed or too large.",
			})

			return
		}

		input := newPlayerInput(r)
		if input.errMsg != "" {
			renderer.Render(w, r, http.StatusBadRequest, playerCreatePageData{
				Title:       "Admin Dashboard - New Player",
				DisplayName: input.DisplayName, Email: input.Email,
				Error: input.errMsg,
			})

			return
		}

		hash, err := auth.HashPassword(input.Password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password for admin-create", slog.Any("err", err))
			renderer.Render(w, r, http.StatusInternalServerError, playerCreatePageData{
				Title:       "Admin Dashboard - New Player",
				DisplayName: input.DisplayName, Email: input.Email,
				Error: "Could not create player. Try again.",
			})

			return
		}

		player, err := store.CreatePlayerByAdmin(r.Context(), input.DisplayName, input.Email, hash, auth.RolePlayer)
		if err != nil {
			renderCreatePlayerError(w, r, renderer, input, err)

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
	w http.ResponseWriter, r *http.Request, renderer *render.Renderer, in newPlayerCreateInput, err error,
) {
	status := http.StatusInternalServerError
	msg := "Could not create player. Try again."
	switch {
	case errors.Is(err, auth.ErrDisplayNameTaken):
		status = http.StatusConflict
		msg = "That display name is already taken."
	case errors.Is(err, auth.ErrEmailTaken):
		status = http.StatusConflict
		msg = "Another account already uses that email."
	default:
		// Fall through to the generic 500 message; err is logged by the caller.
	}
	renderer.Render(w, r, status, playerCreatePageData{
		Title:       "Admin Dashboard - New Player",
		DisplayName: in.DisplayName, Email: in.Email,
		Error: msg,
	})
}
