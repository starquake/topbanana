package profile

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
)

// HandleSignOutEverywhere returns the [http.Handler] for POST
// /profile/sign-out-everywhere. It bumps the player's session_version, which
// signs out every other browser, then re-issues this request's cookie with the
// new version so the current browser stays signed in. It needs no password, so
// it works for accounts that only sign in with Google.
func HandleSignOutEverywhere(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	revoker auth.SessionRevoker,
	sessions *session.Manager,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "sign out everywhere reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing sign out everywhere form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		version, err := revoker.BumpSessionVersion(r.Context(), player.ID)
		if err != nil {
			logger.ErrorContext(r.Context(), "error bumping session version",
				slog.Int64(logPlayerIDKey, player.ID), slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		sessions.Set(w, player.ID, version)
		logger.InfoContext(r.Context(), "signed out everywhere", slog.Int64(logPlayerIDKey, player.ID))

		loc := locale.Resolve(r)
		next := adminNextPath(r.PostFormValue("next"))
		backHref, backLabel := backFromNext(loc, next)
		renderer.render(w, r, http.StatusOK, pageData{
			Title:               locale.Translate(loc, "profile.heading"),
			DisplayName:         player.DisplayName,
			SignedOutEverywhere: true,
			BackHref:            backHref,
			BackLabel:           backLabel,
			Next:                next,
		})
	})
}
