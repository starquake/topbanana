package client

import (
	"context"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
)

const (
	defaultOGTitle       = "Be the Top Banana!"
	defaultOGDescription = "Make a quiz, share the link, see who's the top banana."
	pageTitleSuffix      = " — Top Banana!"
)

// QuizLookup is the subset of the quiz store the per-quiz share handler uses.
type QuizLookup interface {
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
}

// ShellHandlers serves the SPA shell with Open Graph metadata injected per
// request. /client/{$} renders sitewide defaults; /play/{slugID} overrides
// og:title and og:description with the named quiz's own values so chat-app
// link previews (WhatsApp, Slack, Discord, …) surface the quiz a host is
// sharing instead of generic site copy.
type ShellHandlers struct {
	cfg       *config.Config
	quizStore QuizLookup
	logger    *slog.Logger
}

// NewShellHandlers wires the handlers used by /client/{$} and /play/{slugID}.
func NewShellHandlers(cfg *config.Config, quizStore QuizLookup, logger *slog.Logger) *ShellHandlers {
	return &ShellHandlers{cfg: cfg, quizStore: quizStore, logger: logger}
}

// shellData feeds the index.html template. One title value drives both
// <title> and og:title — html/template applies the right escaping per
// context, so a single string is enough.
type shellData struct {
	Title       string
	Description string
}

// Index handles GET /client/{$} — the SPA root with no quiz context. Uses
// the sitewide default Open Graph card.
func (s *ShellHandlers) Index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, shellData{
		Title:       defaultOGTitle,
		Description: defaultOGDescription,
	})
}

// Play handles GET /play/{slugID}. Looks the quiz up by slug-id and injects
// its title and description into the Open Graph card. A missing or
// unparseable slug falls back to sitewide defaults — the SPA itself surfaces
// the missing-quiz state via its own API call, so a degraded preview is
// preferable to a 404 on the share link.
func (s *ShellHandlers) Play(w http.ResponseWriter, r *http.Request) {
	data := shellData{
		Title:       defaultOGTitle,
		Description: defaultOGDescription,
	}

	id, err := handlers.IDFromSlugID(r.PathValue("slugID"))
	if err == nil {
		if q, qerr := s.quizStore.GetQuiz(r.Context(), id); qerr == nil && q != nil {
			data.Title = q.Title + pageTitleSuffix
			if q.Description != "" {
				data.Description = q.Description
			}
		} else if qerr != nil && !errors.Is(qerr, quiz.ErrQuizNotFound) {
			s.logger.ErrorContext(r.Context(), "play share: quiz lookup", slog.Any("err", qerr))
		}
	}

	s.render(w, r, data)
}

// render parses index.html on each request: cheap (~50µs for a ~100-line
// file) and keeps the ClientDir dev path picking up live edits without a
// rebuild. Page loads are infrequent compared to /api/* traffic, so the
// extra allocation is in the noise.
func (s *ShellHandlers) render(w http.ResponseWriter, r *http.Request, data shellData) {
	funcs := template.FuncMap{
		"ogImage": func() string { return absurl.BaseURL(r) + "/assets/og-image.png" },
	}
	t, err := template.New("index.html").Funcs(funcs).ParseFS(s.fsys(), "index.html")
	if err != nil {
		s.logger.ErrorContext(r.Context(), "parse index template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		// Headers are already flushed — log and let the client see a
		// truncated response rather than a stray 500.
		s.logger.ErrorContext(r.Context(), "render index template", slog.Any("err", err))
	}
}

// fsys mirrors Handler: ClientDir override for dev, embedded for prod.
func (s *ShellHandlers) fsys() fs.FS {
	if s.cfg.ClientDir != "" {
		return os.DirFS(s.cfg.ClientDir)
	}
	sub, _ := fs.Sub(staticFS, "static")

	return sub
}
