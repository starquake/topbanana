package demo

import (
	"html/template"
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
// returns next unchanged (zero overhead). When on, it serves GET /demo and
// POST /demo/enter, 404s the blocked paths, and delegates the rest.
func Guard(next http.Handler, deps Deps) http.Handler {
	if !Enabled() {
		return next
	}

	return &handler{
		next:     next,
		sessions: session.New([]byte(deps.Cfg.SessionKey), deps.Cfg.SecureCookies()),
		players:  deps.Players,
		logger:   deps.Logger,
		tmpl:     template.Must(template.New("enter.gohtml").ParseFS(entryTmplFS, "templates/enter.gohtml")),
	}
}

type handler struct {
	next     http.Handler
	sessions *session.Manager
	players  auth.PlayerStore
	logger   *slog.Logger
	tmpl     *template.Template
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/demo" && r.Method == http.MethodGet {
		h.serveEntry(w, r)

		return
	}
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
// Blocked prefixes: account self-service (/profile/*) and real sign-in/signup
// (/register, Google OAuth). Outbound email is not blocked here - the demo
// deployment runs with SMTP unconfigured, so the app already uses a no-op mailer.
func isBlocked(path string) bool {
	for _, p := range []string{"/profile", "/register", "/login/google"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}

	return false
}
