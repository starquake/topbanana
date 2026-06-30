package demo

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

// HandleEnter logs a visitor into the shared demo Host and redirects to the
// admin quiz list. Mounted at POST /demo/enter only when demo mode is on
// (see internal/server/routes.go). CSRF is intentionally not enforced: it
// logs into a shared, daily-wiped public account and a first visit has no
// session/token yet.
func HandleEnter(sessions *session.Manager, players auth.PlayerStore, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := players.GetPlayerByDisplayName(r.Context(), demoHostName)
		if err != nil {
			logger.ErrorContext(r.Context(), "demo enter: host not ready", slog.Any("err", err))
			http.Error(w, "demo host not ready", http.StatusServiceUnavailable)

			return
		}
		sessions.Set(w, host.ID, host.SessionVersion)
		http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
	})
}
