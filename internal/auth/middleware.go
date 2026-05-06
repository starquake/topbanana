package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/session"
)

// RequireAdmin wraps the next handler so only admins can reach it.
//
// If the request has no valid session cookie, the player cannot be found, or the
// player's role is not admin, the request is redirected to /login with HTTP 303.
func RequireAdmin(next http.Handler, players PlayerStore, sessions *session.Manager, logger *slog.Logger) http.Handler {
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
			http.Redirect(w, r, "/login", http.StatusSeeOther)

			return
		}

		next.ServeHTTP(w, r)
	})
}
