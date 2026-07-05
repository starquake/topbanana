package profile

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
)

// passwordPageData feeds profile_password.gohtml. Message renders the
// error banner; Saved renders the success banner. No form values are
// preserved across renders because the three fields are all passwords
// and there is no UX value in retaining what the user typed.
type passwordPageData struct {
	Title   string
	Message string
	Saved   bool
}

// HandleProfilePassword returns the [http.Handler] for GET
// /profile/password. RequireAuthenticated upstream guarantees the
// context carries a *Player.
func HandleProfilePassword(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile_password.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.PlayerFromContext(r.Context()); !ok {
			logger.ErrorContext(r.Context(), "profile password handler reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		title := locale.Translate(locale.Resolve(r), "profile.changePassword")
		render.renderAny(w, r, http.StatusOK, passwordPageData{Title: title})
	})
}

// HandleProfilePasswordChange returns the [http.Handler] for POST
// /profile/password. Verifies the current password, rotates the hash
// (via ChangePlayerPassword - which also bumps session_version so
// every other live cookie for this account is invalidated), and
// refreshes the current request's session cookie with the new
// session_version so the active tab stays signed in.
//
// Timing equalisation is intentionally not applied: the visitor is
// already authenticated, so a slower-on-mismatch response cannot leak
// "this account exists" the way it would on the login form.
func HandleProfilePasswordChange(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players auth.PlayerStore,
	sessions *session.Manager,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile_password.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "profile password change reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing profile password form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		loc := locale.Resolve(r)
		title := locale.Translate(loc, "profile.changePassword")
		current := r.PostFormValue("current_password")
		newPassword := r.PostFormValue("new_password")
		confirm := r.PostFormValue("new_password_confirm")

		if msg, ok := validatePasswordChangeInput(loc, newPassword, confirm); !ok {
			logger.InfoContext(r.Context(), "profile password change rejected: invalid input",
				slog.Int64("player_id", player.ID))
			render.renderAny(w, r, http.StatusBadRequest, passwordPageData{
				Title: title, Message: msg,
			})

			return
		}

		if player.PasswordHash == "" || auth.CheckPassword(player.PasswordHash, current) != nil {
			logger.InfoContext(r.Context(), "profile password change rejected: current password incorrect",
				slog.Int64("player_id", player.ID))
			render.renderAny(w, r, http.StatusUnauthorized, passwordPageData{
				Title: title, Message: locale.Translate(loc, "profile.currentPasswordIncorrect"),
			})

			return
		}

		if !rotateAndRefresh(w, r, logger, players, sessions, player.ID, newPassword) {
			return
		}

		render.renderAny(w, r, http.StatusOK, passwordPageData{
			Title: title,
			Saved: true,
		})
	})
}

// rotateAndRefresh hashes the new password, rotates the row's hash
// (which also bumps session_version - the invalidation that wipes
// every other live cookie for this account), and re-issues the
// current request's cookie with the new version so the active tab
// stays signed in. Returns true on success; on failure it writes the
// error response and returns false so the caller can bail.
//
// Split out of HandleProfilePasswordChange to keep the handler under
// revive's function-length cap. The cookie refresh is intentionally
// scoped here too: it has no business running unless the rotation
// itself succeeded.
func rotateAndRefresh(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	players auth.PlayerStore,
	sessions *session.Manager,
	playerID int64,
	newPassword string,
) bool {
	hashed, err := auth.HashPassword(newPassword)
	if err != nil {
		logger.ErrorContext(r.Context(), "error hashing new password", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return false
	}

	if rotateErr := players.ChangePlayerPassword(r.Context(), playerID, hashed); rotateErr != nil {
		if errors.Is(rotateErr, auth.ErrPlayerNotFound) {
			logger.ErrorContext(r.Context(), "change password: player vanished mid-request",
				slog.Int64("player_id", playerID))
		} else {
			logger.ErrorContext(r.Context(), "error rotating password", slog.Any("err", rotateErr))
		}
		http.Error(w, "internal error", http.StatusInternalServerError)

		return false
	}

	refreshed, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error reloading player after password change", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return false
	}
	sessions.Set(w, refreshed.ID, refreshed.SessionVersion)

	return true
}

// validatePasswordChangeInput pins the same length rule the register
// and reset forms apply, plus a confirm-match check. Returns the
// user-facing banner text (localized for loc) and false when the input
// is rejected.
func validatePasswordChangeInput(loc, password, confirm string) (string, bool) {
	if len(password) < auth.MinPasswordLength {
		return locale.TranslateCount(loc, "validation.passwordTooShort", auth.MinPasswordLength), false
	}
	if len(password) > auth.MaxPasswordLength {
		return locale.TranslateCount(loc, "validation.passwordTooLong", auth.MaxPasswordLength), false
	}
	if password != confirm {
		return locale.Translate(loc, "validation.passwordsNoMatch"), false
	}

	return "", true
}
