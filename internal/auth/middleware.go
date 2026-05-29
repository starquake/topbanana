package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/rs/xid"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
)

// anonymousUsernamePrefix is the prefix used by the last-resort xid-backed
// fallback when GeneratePetname collisions exhaust the retry budget. The
// petname path is the common case; this prefix only appears in the
// astronomically unlikely event that the petname pool becomes saturated or
// the same petname is drawn N times in a row.
const anonymousUsernamePrefix = "anon-"

// petnameMaxAttempts caps how many times EnsurePlayer will retry a petname
// against the UNIQUE-on-username index before falling back to an xid-backed
// name. With ~15M combinations the chance of one collision is tiny and the
// chance of five in a row is effectively zero, so five attempts is a safe
// upper bound that still keeps the request latency bounded.
const petnameMaxAttempts = 5

// EnsurePlayer guarantees the request carries a session pointing at
// an existing players row, creating a fresh anonymous row when
// necessary. Wrap /api/* routes that attribute work to a player;
// static client assets are deliberately not wrapped so loading
// index.html does not create a row.
func EnsurePlayer(next http.Handler, players PlayerStore, sessions *session.Manager, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, err := loadSessionPlayer(r, players, sessions)
		if err != nil && !errors.Is(err, ErrPlayerNotFound) {
			logger.ErrorContext(r.Context(), "error loading player for ensure", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		if player != nil {
			next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))

			return
		}

		// Fall-through from loadSessionPlayer (no cookie or deleted row):
		// the session cookie must be replaced before the next handler runs.
		player, err = mintAnonymousPlayer(r.Context(), players)
		if err != nil {
			logger.ErrorContext(r.Context(), "error creating anonymous player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		sessions.Set(w, player.ID, player.SessionVersion)
		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}

// loadSessionPlayer resolves the player referenced by the request's session
// cookie. It returns (player, nil) when a usable row was found,
// (nil, ErrPlayerNotFound) when there is no session cookie OR the cookie
// referenced a deleted row OR the cookie's session_version stamp does
// not match the row's current session_version (a password reset has
// happened since the cookie was issued, so it must be treated as
// invalidated). Other store failures bubble up as wrapped errors.
func loadSessionPlayer(r *http.Request, players PlayerStore, sessions *session.Manager) (*Player, error) {
	playerID, sessionVersion, hasSession := sessions.Decode(r)
	if !hasSession {
		return nil, ErrPlayerNotFound
	}

	player, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return nil, ErrPlayerNotFound
		}

		return nil, fmt.Errorf("load player by id: %w", err)
	}
	if player.SessionVersion != sessionVersion {
		// Reset bumped session_version after this cookie was minted -
		// surface as not-found so the caller bounces through login.
		return nil, ErrPlayerNotFound
	}

	return player, nil
}

// mintAnonymousPlayer creates a brand-new anonymous players row. The happy
// path takes a fresh petname; on UNIQUE collisions it retries up to
// petnameMaxAttempts times before falling back to an xid-backed name that is
// unique by construction.
func mintAnonymousPlayer(ctx context.Context, players PlayerStore) (*Player, error) {
	var lastErr error
	for range petnameMaxAttempts {
		player, err := players.CreateAnonymousPlayer(ctx, GeneratePetname())
		if err == nil {
			return player, nil
		}
		if !errors.Is(err, ErrUsernameTaken) {
			return nil, fmt.Errorf("create anonymous player: %w", err)
		}
		lastErr = err
	}

	// Petname pool collided every attempt. Fall back to an xid-backed name,
	// which is unique by construction and effectively guarantees the insert
	// succeeds even if the petname pool ever becomes saturated.
	player, err := players.CreateAnonymousPlayer(ctx, anonymousUsernamePrefix+xid.New().String())
	if err != nil {
		return nil, fmt.Errorf("petname exhausted (last: %w); xid fallback: %w", lastErr, err)
	}

	return player, nil
}

// RequireGameHost gates the handler to Hosts and Admins - the dashboard +
// own-game routes (#538). Unauthenticated requests 303 to /login; GET/HEAD
// carry the original URI as ?next=<encoded> so the login flow can drop the
// visitor back where they were heading, but unsafe methods drop next (the form
// body is gone). A signed-in Player (no host rights) gets a 403 "Access
// denied" page, because the dashboard's existence is not a secret.
func RequireGameHost(
	next http.Handler,
	players PlayerStore,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	logger *slog.Logger,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/access_denied.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, err := loadSessionPlayer(r, players, sessions)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				redirectToLoginWithNext(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error loading player for host check", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !player.CanHost() {
			render.render(w, r, http.StatusForbidden, formData{
				Title:    "Access denied",
				Username: player.Username,
			})

			return
		}

		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}

// RequireAdmin gates the handler to Admins only - the top tier (#538):
// player management, role changes, account creation, email diagnostics, and
// the settings console. Unauthenticated requests are handled like
// [RequireGameHost] (303 to /login, carrying ?next= on safe methods). A
// signed-in non-Admin (Player or Host) gets a plain 404 rather than a 403: the
// admin surface is hidden from Hosts, so its routes must not betray their
// existence (#320/#538).
func RequireAdmin(
	next http.Handler,
	players PlayerStore,
	sessions *session.Manager,
	logger *slog.Logger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, err := loadSessionPlayer(r, players, sessions)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				redirectToLoginWithNext(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error loading player for admin check", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !player.IsAdmin() {
			http.NotFound(w, r)

			return
		}

		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}

// RequireAuthenticated wraps the next handler so only credentialled
// players (password, OAuth identity, or the seeded admin role) can
// reach it. Anonymous-session visitors and cookieless requests are
// redirected to /login with HTTP 303 - softer than RequireAdmin's
// 403, because the page they're missing is typically reachable for
// them after they sign in (the profile page, future personal
// dashboards, etc.). The original URI is carried as ?next=<encoded>
// on GET/HEAD; see [RequireAdmin] for the rationale. Stashes the
// loaded *Player on the request context via WithPlayer so downstream
// handlers can read it without a second lookup.
func RequireAuthenticated(
	next http.Handler,
	players PlayerStore,
	sessions *session.Manager,
	logger *slog.Logger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, err := loadSessionPlayer(r, players, sessions)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				redirectToLoginWithNext(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error loading player for authn check", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !player.IsAuthenticated() {
			redirectToLoginWithNext(w, r)

			return
		}

		next.ServeHTTP(w, r.WithContext(WithPlayer(r.Context(), player)))
	})
}

// redirectToLoginWithNext 303s to /login with a ?next= query carrying
// the original URI when the request method is safe to re-issue after
// login (GET/HEAD). POSTs and other unsafe methods drop next because
// the form body cannot be replayed - the visitor lands on the bare
// /login page and re-navigates to their destination by hand.
//
// Only paths accepted by [SafeNextPath] are forwarded; anything else
// is dropped so the parameter cannot be abused as an open-redirect
// vector.
func redirectToLoginWithNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Redirect(w, r, "/login", http.StatusSeeOther)

		return
	}
	target := SafeNextPath(r.URL.RequestURI())
	if target == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)

		return
	}
	u := url.URL{Path: "/login", RawQuery: "next=" + url.QueryEscape(target)}
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
