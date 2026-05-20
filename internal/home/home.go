// Package home renders the public start page at GET /. It surfaces the
// most-played quizzes from the last 30 days and the most active players,
// alongside a discreet link into the admin dashboard. The page is
// server-rendered HTML; no auth required.
package home

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/web/tmpl"
)

// maxItems caps how many popular quizzes and active players the page
// renders. The underlying queries return everything ranked; we slice to
// this size after fetching because sqlc's SQLite parser does not accept
// a LIMIT placeholder in multi-query files. The number is intentionally
// modest so the page stays scannable on a phone.
const maxItems = 6

// PopularQuiz is one row in the "popular quizzes" list. PlayCount is the
// number of finished games over the last 30 days; the template uses it
// to render a "N plays" pill alongside the title.
type PopularQuiz struct {
	ID          int64
	Title       string
	Slug        string
	Description string
	PlayCount   int
}

// PlayURL is the share-able deep link the home page card points at.
// Mirrors the per-quiz share path the admin list uses.
func (p PopularQuiz) PlayURL() string {
	return fmt.Sprintf("/play/%s-%d", p.Slug, p.ID)
}

// ActivePlayer is one row in the "most active players" list.
// FinishedCount is the number of finished games the player has across
// all quizzes; the template renders it as a coarse activity score.
type ActivePlayer struct {
	ID            int64
	Username      string
	FinishedCount int
}

// Store is the read-only data dependency the home handler needs.
// Implemented by store.HomeStore against the real database; tests can
// substitute a stub that returns canned rows.
type Store interface {
	ListPopularQuizzes(ctx context.Context) ([]*PopularQuiz, error)
	ListMostActivePlayers(ctx context.Context) ([]*ActivePlayer, error)
}

// pageData is the render-time payload for index.gohtml. Top-N slices
// are bounded at [maxItems] so the handler never feeds the template a
// pathologically long list even if the store returns one.
type pageData struct {
	Title          string
	PopularQuizzes []*PopularQuiz
	ActivePlayers  []*ActivePlayer
}

// Handle returns the [http.Handler] for GET /. The template tree is
// parsed once per call to Handle (at server start) and re-cloned per
// request so html/template's context-aware escaping applies cleanly to
// each render. Errors fetching either list degrade gracefully: the page
// renders an empty state for the failing section so the admin link
// stays reachable even if the database is having a bad day.
func Handle(logger *slog.Logger, store Store) http.Handler {
	t := parseTemplate()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := pageData{Title: "Top Banana!"}

		quizzes, err := store.ListPopularQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list popular quizzes", slog.Any("err", err))
		} else {
			data.PopularQuizzes = truncate(quizzes)
		}

		players, err := store.ListMostActivePlayers(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list most active players", slog.Any("err", err))
		} else {
			data.ActivePlayers = truncate(players)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
			logger.ErrorContext(r.Context(), "render home template", slog.Any("err", err))
		}
	})
}

// truncate caps the slice at [maxItems]. The home page lists are
// presentational, so any element past the cap is unreachable from the
// rendered UI; slicing in Go keeps the SQL pageable and lets both
// list types share one helper.
func truncate[T any](rows []T) []T {
	if len(rows) > maxItems {
		return rows[:maxItems]
	}

	return rows
}

// parseTemplate loads the home layout + page templates from the embedded
// tmpl.FS. The {{add}} func renders a 1-based rank next to each entry
// in the active-players list; html/template has no arithmetic builtin,
// so it is registered here.
func parseTemplate() *template.Template {
	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}
	base := template.Must(
		template.New("").Funcs(funcs).ParseFS(tmplFS(), "home/layouts/*.gohtml"),
	)

	return template.Must(base.ParseFS(tmplFS(), "home/pages/*.gohtml"))
}

// tmplFS exposes the embedded template FS as [fs.FS] so the parse
// helpers can take it as an interface. Kept as a tiny helper to make
// future swaps (e.g. a dev-mode disk override) a one-line change.
func tmplFS() fs.FS {
	return tmpl.FS
}
