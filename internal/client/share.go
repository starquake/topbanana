package client

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/quiz"
)

const (
	defaultOGTitle       = "Be the Top Banana!"
	defaultOGDescription = "Make a quiz, share the link, see who's the top banana."
	pageTitleSuffix      = " - Top Banana!"
)

// QuizLookup is the subset of the quiz store the per-quiz share handler uses.
type QuizLookup interface {
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
}

// ShellHandlers serves the SPA shell with Open Graph metadata injected per
// request. /client/{$} renders sitewide defaults; /play/{slugID} overrides
// og:title and og:description with the named quiz's own values so chat-app
// link previews (WhatsApp, Slack, Discord, ...) surface the quiz a host is
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

// shellData feeds the index.gohtml template. One title value drives both
// <title> and og:title - html/template applies the right escaping per
// context, so a single string is enough.
//
// Locale sets <html lang> and window.__I18N__.locale; MessagesJSON is the
// merged catalog injected as window.__I18N__.messages so the SPA needs no
// fetch. Both are set by render from the request, not by the per-URL callers.
type shellData struct {
	Title               string
	Description         string
	RegistrationEnabled bool
	Locale              string
	MessagesJSON        template.JS
}

// Index handles GET /client/{$} - the SPA root with no quiz context. Uses
// the sitewide default Open Graph card.
func (s *ShellHandlers) Index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index.gohtml", shellData{
		Title:               defaultOGTitle,
		Description:         defaultOGDescription,
		RegistrationEnabled: s.cfg.RegistrationEnabled,
	})
}

// Join handles GET /join and GET /join/{code} - the player join + lobby
// surface (MP-4 / #681). It renders join.gohtml with the sitewide Open Graph
// card: a room code is short-lived and per-session, so a shared /join link
// must not leak a live game into link previews. The room code is read from
// the URL client-side; the shell carries no per-session data.
func (s *ShellHandlers) Join(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "join.gohtml", shellData{
		Title:               defaultOGTitle,
		Description:         defaultOGDescription,
		RegistrationEnabled: s.cfg.RegistrationEnabled,
	})
}

// Play handles GET /play/{slugID}. Looks the quiz up by slug-id and injects
// its title and description into the Open Graph card. A missing or
// unparseable slug falls back to sitewide defaults - the SPA itself surfaces
// the missing-quiz state via its own API call, so a degraded preview is
// preferable to a 404 on the share link.
func (s *ShellHandlers) Play(w http.ResponseWriter, r *http.Request) {
	data := shellData{
		Title:               defaultOGTitle,
		Description:         defaultOGDescription,
		RegistrationEnabled: s.cfg.RegistrationEnabled,
	}

	if id, err := handlers.IDFromSlugID(r.PathValue("slugID")); err == nil {
		s.applyQuizOG(r, id, &data)
	}

	s.render(w, r, "index.gohtml", data)
}

// applyQuizOG overrides the share card's title/description with the named quiz's
// own values, but keeps the sitewide defaults for a quiz that is missing, live,
// private, or a draft: none is a publicly-playable solo quiz, so surfacing its
// details to anonymous scrapers would spoiler a hosted game (#677) or leak a
// non-public quiz (#103, #1192). All keep the default card, not a 404 (#678).
func (s *ShellHandlers) applyQuizOG(r *http.Request, id int64, data *shellData) {
	q, err := s.quizStore.GetQuiz(r.Context(), id)
	if err != nil {
		if !errors.Is(err, quiz.ErrQuizNotFound) {
			s.logger.ErrorContext(r.Context(), "play share: quiz lookup", slog.Any("err", err))
		}

		return
	}
	if q == nil || q.Mode == quiz.ModeLive || q.Visibility == quiz.VisibilityPrivate || !q.Published {
		return
	}

	data.Title = q.Title + pageTitleSuffix
	if q.Description != "" {
		data.Description = q.Description
	}
}

// render parses the named shell template on each request: cheap (~50us for a
// ~100-line file) and keeps the ClientDir dev path picking up live edits
// without a rebuild. Page loads are infrequent compared to /api/* traffic, so
// the extra allocation is in the noise.
func (s *ShellHandlers) render(w http.ResponseWriter, r *http.Request, name string, data shellData) {
	loc := locale.Resolve(r)
	data.Locale = loc
	data.MessagesJSON = locale.MessagesJSON(loc)
	funcs := template.FuncMap{
		"ogImage":     func() string { return absurl.BaseURL(r) + "/static/og-image.png" },
		"envTitleTag": envtag.Get,
		"t":           func(key string) string { return locale.Translate(loc, locale.MessageID(key)) },
		"lang":        func() string { return loc },
	}
	// partials/ holds the {{define}} blocks shared between index.gohtml (solo) and
	// join.gohtml (live): round_intro ("round-intro-card"), standings_bars
	// ("standings-bars"), brand_mark ("brand-mark"), and lang_switcher
	// ("lang-switcher"). Parsing them alongside every shell keeps both able to
	// invoke the partials; a missing or renamed file fails loudly here rather than
	// rendering a blank surface.
	t, err := template.New(name).Funcs(funcs).ParseFS(subFS(s.cfg, "tmpl"), name,
		"partials/round_intro.gohtml", "partials/standings_bars.gohtml", "partials/brand_mark.gohtml",
		"partials/lang_switcher.gohtml")
	if err != nil {
		s.logger.ErrorContext(r.Context(), "parse shell template", slog.Any("err", err), slog.String("template", name))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		// Headers are already flushed - log and let the client see a
		// truncated response rather than a stray 500.
		s.logger.ErrorContext(r.Context(), "render shell template", slog.Any("err", err), slog.String("template", name))
	}
}
