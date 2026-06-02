// Package home renders the public-facing pages at GET / and GET
// /quizzes. The start page (/) surfaces popular quizzes + most active
// players; the all-quizzes page (#284) surfaces every visible quiz so
// niche or recently-authored quizzes are findable beyond the top-six
// popular list. Both are server-rendered HTML and require no auth.
package home

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/quiz"
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

// NewestQuiz is one row in the "newest quizzes" list. QuestionCount is
// how many questions the quiz has; the template renders it as a
// "N questions" pill in place of the popular list's play count.
type NewestQuiz struct {
	ID            int64
	Title         string
	Slug          string
	Description   string
	QuestionCount int
}

// PlayURL is the share-able deep link the home page card points at.
// Mirrors [PopularQuiz.PlayURL] so a quiz reached from either tab picks
// up the same share path.
func (n NewestQuiz) PlayURL() string {
	return fmt.Sprintf("/play/%s-%d", n.Slug, n.ID)
}

// Viewer is the slice of the signed-in player the home layout needs to
// render the "Signed in as X | Log out" footer affordance. Nil when
// the request is anonymous (no session, or a session pointing at an
// EnsurePlayer auto-petname row).
type Viewer struct {
	DisplayName string
}

// ViewerFunc resolves the signed-in player for a request, or returns
// nil when the request is anonymous. The home handler invokes this
// per-request; keeping it a function (not an interface) lets the
// wiring layer pull the session + player store dependencies together
// without home having to import either internal/auth or internal/store.
type ViewerFunc func(r *http.Request) *Viewer

// CSRFTokenFunc returns a CSRF token bound to the request's cookie.
// Used to populate the hidden field of the footer's log-out form so
// the CSRF middleware on POST /logout accepts the submission. Same
// "pass a function, don't import the csrf package" rationale as
// [ViewerFunc].
type CSRFTokenFunc func(w http.ResponseWriter, r *http.Request) string

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
	DisplayName   string
	FinishedCount int
}

// Store is the read-only data dependency the home handler needs.
// Implemented by store.HomeStore against the real database; tests can
// substitute a stub that returns canned rows.
type Store interface {
	ListPopularQuizzes(ctx context.Context) ([]*PopularQuiz, error)
	ListNewestQuizzes(ctx context.Context) ([]*NewestQuiz, error)
	ListMostActivePlayers(ctx context.Context) ([]*ActivePlayer, error)
}

// pageData is the render-time payload for index.gohtml. Top-N slices
// are bounded at [maxItems] so the handler never feeds the template a
// pathologically long list even if the store returns one. Viewer is
// nil for anonymous requests; the base template renders the footer's
// log-out affordance only when non-nil.
type pageData struct {
	Title          string
	PopularQuizzes []*PopularQuiz
	NewestQuizzes  []*NewestQuiz
	ActivePlayers  []*ActivePlayer
	Viewer         *Viewer
}

// Handle returns the GET / handler. List-fetch errors degrade
// gracefully to an empty state so the admin link stays reachable.
// When viewer and csrfToken are both nil the footer renders "Log in"
// instead of the signed-in affordance.
func Handle(
	logger *slog.Logger,
	store Store,
	viewer ViewerFunc,
	csrfToken CSRFTokenFunc,
) http.Handler {
	t := parseTemplate("home/pages/index.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := pageData{Title: "Top Banana!"}
		if viewer != nil {
			data.Viewer = viewer(r)
		}

		quizzes, err := store.ListPopularQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list popular quizzes", slog.Any("err", err))
		} else {
			data.PopularQuizzes = truncate(quizzes)
		}

		newest, err := store.ListNewestQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list newest quizzes", slog.Any("err", err))
		} else {
			data.NewestQuizzes = truncate(newest)
		}

		players, err := store.ListMostActivePlayers(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list most active players", slog.Any("err", err))
		} else {
			data.ActivePlayers = truncate(players)
		}

		executeTemplate(w, r, logger, t, csrfToken, "render home template", data)
	})
}

// QuizLister is the read-only data dependency the all-quizzes handler
// needs. Implemented by store.QuizStore (already exposes both methods
// for the admin list); a separate interface keeps the home package
// free of any direct dependency on the *store package.
type QuizLister interface {
	ListPublicQuizzes(ctx context.Context) ([]*quiz.Quiz, error)
	QuestionCountsByQuiz(ctx context.Context) (map[int64]int, error)
}

// AllQuizRow is one row in the /quizzes list. Strictly a presentation
// type - no behaviour beyond [AllQuizRow.PlayURL] - so the template
// doesn't need to know anything about quiz.Quiz internals.
type AllQuizRow struct {
	ID                   int64
	Title                string
	Slug                 string
	Description          string
	QuestionCount        int
	CreatedByDisplayName string
}

// PlayURL is the share-able deep link the row card points at. Mirrors
// [PopularQuiz.PlayURL] so a player landing on /quizzes vs the home
// page picks up the same share path.
func (a AllQuizRow) PlayURL() string {
	return fmt.Sprintf("/play/%s-%d", a.Slug, a.ID)
}

// allQuizzesData backs all-quizzes.gohtml. The slice can be empty when
// no quizzes exist yet - the template renders an empty-state message
// rather than a bare page. Viewer wires the same footer affordance as
// the home page (see [pageData.Viewer]).
type allQuizzesData struct {
	Title   string
	Quizzes []*AllQuizRow
	Viewer  *Viewer
}

// HandleAllQuizzes returns the [http.Handler] for GET /quizzes (#284).
// Lists every public quiz, most-recently-updated first, so newly-authored
// or off-the-30-day-window quizzes stay findable. Unlisted and private
// quizzes are excluded by the visibility gate (#103). Question counts
// come from a single QuestionCountsByQuiz call to avoid the N+1 a per-row
// lookup would produce. A failure in either underlying query renders the
// page with the empty list rather than 500-ing - the admin link in the
// footer stays reachable.
func HandleAllQuizzes(
	logger *slog.Logger,
	store QuizLister,
	viewer ViewerFunc,
	csrfToken CSRFTokenFunc,
) http.Handler {
	t := parseTemplate("home/pages/all-quizzes.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := allQuizzesData{Title: "All quizzes - Top Banana!"}
		if viewer != nil {
			data.Viewer = viewer(r)
		}

		quizzes, err := store.ListPublicQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "list public quizzes", slog.Any("err", err))
		}
		counts, err := store.QuestionCountsByQuiz(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "question counts by quiz", slog.Any("err", err))
			counts = map[int64]int{}
		}

		data.Quizzes = make([]*AllQuizRow, 0, len(quizzes))
		for _, qz := range quizzes {
			data.Quizzes = append(data.Quizzes, &AllQuizRow{
				ID:                   qz.ID,
				Title:                qz.Title,
				Slug:                 qz.Slug,
				Description:          qz.Description,
				QuestionCount:        counts[qz.ID],
				CreatedByDisplayName: qz.CreatedByDisplayName,
			})
		}

		executeTemplate(w, r, logger, t, csrfToken, "render all-quizzes template", data)
	})
}

// executeTemplate clones t, binds the per-request funcs, and runs
// base.gohtml. The clone is mandatory: concurrent renders race on the
// shared template tree without it (#294).
func executeTemplate(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	t *template.Template, csrfToken CSRFTokenFunc, errMsg string, data any,
) {
	rt, cerr := t.Clone()
	if cerr != nil {
		logger.ErrorContext(r.Context(), "clone template", slog.Any("err", cerr))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}
	funcs := template.FuncMap{
		"ogImage": func() string { return absurl.BaseURL(r) + "/assets/og-image.png" },
	}
	if csrfToken != nil {
		funcs["csrfToken"] = func() string { return csrfToken(w, r) }
	}
	rt = rt.Funcs(funcs)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := rt.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		logger.ErrorContext(r.Context(), errMsg, slog.Any("err", err))
	}
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

// parseTemplate loads the home layout plus one page template from the
// embedded tmpl.FS. Each page declares its own `content` block, so
// parsing the layout + a single page keeps the block definitions
// unambiguous - a glob across pages would clobber `content` to whatever
// file came last (#284 added the second page).
//
// The {{add}} func renders a 1-based rank next to each entry in the
// active-players list; html/template has no arithmetic builtin, so it
// is registered here.
func parseTemplate(page string) *template.Template {
	funcs := template.FuncMap{
		"add":         func(a, b int) int { return a + b },
		"ogImage":     func() string { return "" },
		"csrfToken":   func() string { return "" },
		"envTitleTag": envtag.Get,
	}
	base := template.Must(
		template.New("").Funcs(funcs).ParseFS(tmplFS(), "home/layouts/*.gohtml"),
	)

	return template.Must(base.ParseFS(tmplFS(), page))
}

// tmplFS exposes the embedded template FS as [fs.FS] so the parse
// helpers can take it as an interface. Kept as a tiny helper to make
// future swaps (e.g. a dev-mode disk override) a one-line change.
func tmplFS() fs.FS {
	return tmpl.FS
}
