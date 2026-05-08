package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/rs/xid"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
)

// anonymousUsernamePrefix is prepended to the random xid used as the username
// for an EnsurePlayer-created row. Keeping a recognisable prefix means an
// admin scrolling through the players table can tell a never-claimed visitor
// apart from a real registration without inspecting password_hash.
const anonymousUsernamePrefix = "anon-"

// EnsurePlayer guarantees the request carries a session that points at an
// existing players row. If the request has no session, an invalid one, or a
// session whose player has been deleted, the middleware creates a fresh
// anonymous players row, writes a session cookie for it, and stashes the
// loaded *Player on the request context so downstream handlers can read it
// via PlayerFromContext.
//
// Wrap every /api/* route that needs to attribute work to a player (creating
// games, submitting answers). Static client assets are deliberately not
// wrapped so loading index.html and JS bundles does not create a row — the
// row is only created on the first /api/ call.
//
// On failure to create a row the request is rejected with 500: proceeding
// without a session would leave the handler unable to attribute writes.
func EnsurePlayer(next http.Handler, players PlayerStore, sessions *session.Manager, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if playerID, ok := sessions.PlayerID(r); ok {
			player, err := players.GetPlayerByID(r.Context(), playerID)
			if err == nil {
				next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))

				return
			}
			if !errors.Is(err, ErrPlayerNotFound) {
				logger.ErrorContext(r.Context(), "error loading player for ensure", slog.Any("err", err))
				http.Error(w, "internal error", http.StatusInternalServerError)

				return
			}
			// Fall through: the cookie referenced a deleted row, mint a new
			// anonymous player and replace the cookie.
		}

		username := anonymousUsernamePrefix + xid.New().String()
		player, err := players.CreateAnonymousPlayer(r.Context(), username)
		if err != nil {
			logger.ErrorContext(r.Context(), "error creating anonymous player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		sessions.Set(w, player.ID)
		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}

// RequireAdmin wraps the next handler so only admins can reach it.
//
// Unauthenticated requests (no cookie, invalid cookie, or unknown player ID) are
// redirected to /login with HTTP 303. Requests from a valid non-admin session
// receive HTTP 403 with an "Access denied" page so the user understands the
// rejection is about role, not authentication.
//
// The csrfMgr is threaded through the access-denied renderer so the embedded
// "Sign out and switch accounts" form has a working CSRF token.
func RequireAdmin(
	next http.Handler,
	players PlayerStore,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	logger *slog.Logger,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/access_denied.gohtml")

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

		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}
