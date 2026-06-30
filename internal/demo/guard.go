package demo

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/session"
)

// Deps are the collaborators Guard needs. Guard builds its own stateless
// session.Manager from Cfg (same key signs identical cookies), so the session
// manager need not be threaded in from the route wiring.
type Deps struct {
	Cfg     *config.Config
	Players auth.PlayerStore
	Logger  *slog.Logger
}

// Guard wraps the app handler with demo-mode behavior. When demo mode is off it
// returns next unchanged (zero overhead). When on, it serves POST /demo/enter,
// 404s /profile and its subpaths, and delegates the rest.
func Guard(next http.Handler, deps Deps) http.Handler {
	if !Enabled() {
		return next
	}

	return &handler{
		next:     next,
		sessions: session.New([]byte(deps.Cfg.SessionKey), deps.Cfg.SecureCookies()),
		players:  deps.Players,
		logger:   deps.Logger,
	}
}

type handler struct {
	next     http.Handler
	sessions *session.Manager
	players  auth.PlayerStore
	logger   *slog.Logger
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/demo/enter" && r.Method == http.MethodPost {
		h.enter(w, r)

		return
	}
	if isBlocked(r.URL.Path) {
		http.NotFound(w, r)

		return
	}

	h.next.ServeHTTP(w, r)
}

// isBlocked reports whether path matches a blocked prefix exactly or as a
// segment boundary (so /profile and /profile/password match, /profiles does not).
// Blocked prefix: account self-service (/profile/*).
// Outbound email is not blocked here - the demo deployment runs with SMTP
// unconfigured, so the app already uses a no-op mailer.
func isBlocked(path string) bool {
	for _, p := range []string{"/profile"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}

	return false
}

// enter logs the visitor into the shared demo Host by establishing a session
// server-side (no password - the same mechanism OAuth uses), then redirects to
// the host dashboard. CSRF is intentionally not required: it logs into a shared,
// daily-wiped public account with no sensitive target.
func (h *handler) enter(w http.ResponseWriter, r *http.Request) {
	host, err := h.players.GetPlayerByDisplayName(r.Context(), demoHostName)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "demo enter: host not ready", slog.Any("err", err))
		http.Error(w, "demo host not ready", http.StatusServiceUnavailable)

		return
	}
	h.sessions.Set(w, host.ID, host.SessionVersion)
	http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
}
