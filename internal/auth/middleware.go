package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/session"
)

// RequireAdmin wraps the next handler so only admins can reach it.
//
// Unauthenticated requests (no cookie, invalid cookie, or unknown player ID) are
// redirected to /login with HTTP 303. Requests from a valid non-admin session
// receive HTTP 403 with an "Access denied" page so the user understands the
// rejection is about role, not authentication.
func RequireAdmin(next http.Handler, players PlayerStore, sessions *session.Manager, logger *slog.Logger) http.Handler {
	render := newTemplateRenderer(logger, "auth/pages/access_denied.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		playerID, ok := sessions.PlayerID(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)

			return
		}

		player, err := players.GetPlayerByID(r.Context(), playerID)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)

				return
			}
			logger.ErrorContext(r.Context(), "error loading player for admin check", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if player.Role != RoleAdmin {
			render.render(w, r, http.StatusForbidden, formData{
				Title:    "Access denied",
				Username: player.Username,
			})

			return
		}

		next.ServeHTTP(w, r)
	})
}
