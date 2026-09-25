package profile

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
)

// HandleSignOutOtherDevices returns the [http.Handler] for POST
// /profile/sign-out-other-devices. It bumps the player's session_version, which
// signs out every other browser, then re-issues this request's cookie with the
// new version so the current browser stays signed in. It needs no password, so
// it works for accounts that only sign in with Google. It redirects to
// /profile with a flashed notice, so a refresh does not re-post.
func HandleSignOutOtherDevices(
	logger *slog.Logger,
	revoker auth.SessionRevoker,
	sessions *session.Manager,
	flash *auth.SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "sign out other devices reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing sign out other devices form", slog.Any("err", err))
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
		logger.InfoContext(r.Context(), "signed out other devices", slog.Int64(logPlayerIDKey, player.ID))

		flash.SetNotice(w, locale.Translate(locale.Resolve(r), "profile.signedOutOtherDevices"))
		target := "/profile"
		if next := adminNextPath(r.PostFormValue("next")); next != "" {
			target += "?" + url.Values{"next": {next}}.Encode()
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}
