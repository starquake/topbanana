package demo

import (
	"embed"
	"log/slog"
	"net/http"
)

//go:embed templates/enter.gohtml
var entryTmplFS embed.FS

// serveEntry renders the demo splash: the reset banner, the Enter-demo form, and
// a play link.
func (h *handler) serveEntry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, nil); err != nil {
		h.logger.ErrorContext(r.Context(), "demo entry render", slog.Any("err", err))
	}
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
