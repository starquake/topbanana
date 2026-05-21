// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gosimple/slug"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// Validator is an interface for validating data.
type Validator interface {
	Valid(ctx context.Context) map[string]string
}

// TemplateRenderer renders templates using the given logger and template path.
type TemplateRenderer struct {
	logger *slog.Logger
	csrf   *csrf.Manager
	t      *template.Template
}

// NewTemplateRenderer creates a new TemplateRenderer with the given logger,
// CSRF manager, and template path. It parses the template on creation.
//
// The CSRF manager may be nil for callers that render error pages without an
// embedded form (the placeholder {{csrfToken}} func still resolves to "").
func NewTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, templatePath string) *TemplateRenderer {
	return &TemplateRenderer{
		logger: logger,
		csrf:   csrfMgr,
		t:      parseTemplate(templatePath),
	}
}

// Render renders the full base layout with the supplied data. It does not
// return an error because the headers have already been written by the
// time ExecuteTemplate runs — an error page is no longer an option, so
// failures are logged.
//
// The clone-and-override dance behind prepare lets the navbar template
// call {{currentUser}} and any form call {{csrfToken}} without every
// handler having to thread those values into its data struct.
func (tr *TemplateRenderer) Render(w http.ResponseWriter, r *http.Request, status int, data any) {
	t, ok := tr.prepare(w, r)
	if !ok {
		return
	}

	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}

// RenderPartial executes a named template (typically a partial from
// admin/partials/) instead of the full base layout. Used by HTMX-aware
// handlers that want to return only the fragment that needs swapping.
func (tr *TemplateRenderer) RenderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := tr.prepare(w, r)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		tr.logger.ErrorContext(
			r.Context(),
			"error executing partial template",
			slog.String("name", name),
			slog.Any("err", err),
		)
	}
}

// prepare clones the renderer's template tree and binds per-request
// implementations of the {{currentUser}} and {{csrfToken}} funcs that
// parseTemplate registered as placeholders. Returns the prepared template
// and true on success; on Clone failure it surfaces 500 Internal Server
// Error and returns false so the caller can early-return.
//
// The csrf.Token call must run before any WriteHeader because setting the
// nonce cookie is a header write — callers must defer their own header
// writes until after prepare returns.
func (tr *TemplateRenderer) prepare(w http.ResponseWriter, r *http.Request) (*template.Template, bool) {
	t, err := tr.t.Clone()
	if err != nil {
		tr.logger.ErrorContext(r.Context(), "error cloning template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	username := ""
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		username = p.Username
	}

	csrfToken := ""
	if tr.csrf != nil {
		csrfToken = tr.csrf.Token(w, r)
	}

	return t.Funcs(template.FuncMap{
		"currentUser": func() string { return username },
		"csrfToken":   func() string { return csrfToken },
	}), true
}

// QuizData is the data for the quiz list page, it shows multiple
// quizzes when available. CanEdit is the resolved
// "current-session-admin == creator" decision so the templates and
// the questions_list partial do not have to recompute the rule (#281)
// — handlers populate it via [attachCanEdit] before rendering, and a
// rule change lives entirely in Go.
type QuizData struct {
	ID                int64
	Title             string
	Slug              string
	Description       string
	UpdatedAt         time.Time
	QuestionCount     int
	CreatedByPlayerID int64
	CreatedByUsername string
	CanEdit           bool
	Questions         []*QuestionData
}

// QuestionData is the data for a question.
type QuestionData struct {
	ID       int64
	QuizID   int64
	Text     string
	ImageURL string
	Position int
	Options  []*OptionData
}

// OptionData is the data for an option.
type OptionData struct {
	ID         int64
	QuestionID int64
	Text       string
	Correct    bool
	Position   int
}

const (
	maxOptions  = 4
	maxFormSize = 1 << 20 // 1 MB
)

// canEditQuiz is the single source of truth for the creator-only-edit
// rule (#281): the session player must be present and must match the
// quiz's CreatedByPlayerID. Both [attachCanEdit] (read paths) and
// [requireQuizOwner] (mutating paths) call this so the policy lives
// in one place — a future change (additional roles, transferred
// ownership, etc.) only touches this function.
func canEditQuiz(r *http.Request, createdByPlayerID int64) bool {
	p, ok := auth.PlayerFromContext(r.Context())

	return ok && p.ID == createdByPlayerID
}

// attachCanEdit stamps qzd.CanEdit from the session player so templates
// can render the per-row affordances directly without recomputing the
// rule.
func attachCanEdit(r *http.Request, qzd *QuizData) {
	if qzd == nil {
		return
	}
	qzd.CanEdit = canEditQuiz(r, qzd.CreatedByPlayerID)
}

func quizDataFromQuiz(qz *quiz.Quiz) *QuizData {
	// QuestionCount defaults to len(Questions); the list handler overrides
	// it from a separate count query because ListQuizzes doesn't load the
	// question tree.
	return &QuizData{
		ID:                qz.ID,
		Title:             qz.Title,
		Slug:              qz.Slug,
		Description:       qz.Description,
		UpdatedAt:         qz.UpdatedAt,
		QuestionCount:     len(qz.Questions),
		CreatedByPlayerID: qz.CreatedByPlayerID,
		CreatedByUsername: qz.CreatedByUsername,
		Questions:         questionDataFromQuestions(qz.Questions),
	}
}

func quizDataFromQuizzes(quizzes []*quiz.Quiz) []*QuizData {
	data := make([]*QuizData, 0, len(quizzes))
	for _, qz := range quizzes {
		data = append(data, quizDataFromQuiz(qz))
	}

	return data
}

func questionDataFromQuestion(q *quiz.Question) *QuestionData {
	return &QuestionData{
		ID:       q.ID,
		QuizID:   q.QuizID,
		Text:     q.Text,
		ImageURL: q.ImageURL,
		Position: q.Position,
		Options:  optionDataFromOptions(q.Options),
	}
}

func questionDataFromQuestions(questions []*quiz.Question) []*QuestionData {
	data := make([]*QuestionData, 0, len(questions))
	for _, q := range questions {
		data = append(data, questionDataFromQuestion(q))
	}

	slices.SortFunc(
		data,
		func(a, b *QuestionData) int { return a.Position - b.Position },
	)

	return data
}

func optionDataFromOption(op *quiz.Option) *OptionData {
	return &OptionData{
		ID:         op.ID,
		QuestionID: op.QuestionID,
		Text:       op.Text,
		Correct:    op.Correct,
	}
}

func optionDataFromOptions(options []*quiz.Option) []*OptionData {
	data := make([]*OptionData, 0, len(options))
	for _, op := range options {
		data = append(data, optionDataFromOption(op))
	}

	slices.SortFunc(
		data,
		func(a, b *OptionData) int { return a.Position - b.Position },
	)

	return data
}

// parseTemplate parses a template from the given path with layouts.
//
// Placeholder "currentUser" and "csrfToken" funcs are registered before parse
// so the navbar's {{currentUser}} call and any form's {{csrfToken}} call
// resolve at parse time. TemplateRenderer.Render clones the parsed tree and
// replaces these placeholders with implementations that read the request
// context and CSRF manager, respectively.
//
// "humanizeTime" is a pure function of its argument, so it's registered with
// its real implementation here — no per-request override needed.
func parseTemplate(path string) *template.Template {
	funcs := template.FuncMap{
		"currentUser":  func() string { return "" },
		"csrfToken":    func() string { return "" },
		"humanizeTime": humanizeTime,
	}
	base := template.Must(
		template.New("").Funcs(funcs).ParseFS(tmpl.FS, "admin/layouts/*.gohtml"),
	)
	// Partials are pulled in alongside layouts so any page (or any
	// HTMX-fragment handler) can {{template "name" .}} a shared block
	// without re-listing it per-call site.
	base = template.Must(base.ParseFS(tmpl.FS, "admin/partials/*.gohtml"))

	return template.Must(template.Must(base.Clone()).ParseFS(tmpl.FS, path))
}

// hoursPerDay is the bucket size for switching humanizeTime from hours to days.
const hoursPerDay = 24

// humanizeTime returns a coarse relative-time string for t (e.g. "3 hr ago").
// It rounds down to the largest matching bucket and uses absolute zero-handling
// for "just now" so a freshly written record renders sensibly.
func humanizeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}

		return fmt.Sprintf("%d min ago", m)
	case d < hoursPerDay*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hr ago"
		}

		return fmt.Sprintf("%d hr ago", h)
	default:
		days := int(d.Hours() / hoursPerDay)
		if days == 1 {
			return "1 day ago"
		}

		return fmt.Sprintf("%d days ago", days)
	}
}

// render400 renders the 400 error page with the given message.
// Should be used as the final handler in the chain and probably be followed by a return.
//
// Error pages embed the navbar (which contains the logout form), so they need
// a CSRF manager to render a working {{csrfToken}}. We accept it as a
// parameter rather than re-derive it because error renderers are spawned ad
// hoc deep in the call stack — passing it explicitly keeps the rendering path
// honest about its dependencies.
func render400(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager, msg string) {
	render := &TemplateRenderer{logger: logger, csrf: csrfMgr, t: parseTemplate("admin/errors/400.gohtml")}
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Error",
		Message: msg,
	}
	render.Render(w, r, http.StatusBadRequest, data)
}

// render404 renders the 404 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render404(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager) {
	render := &TemplateRenderer{logger: logger, csrf: csrfMgr, t: parseTemplate("admin/errors/404.gohtml")}
	render.Render(w, r, http.StatusNotFound, nil)
}

// render403 renders the 403 error page with a message that names the
// quiz the caller tried to modify and the admin who owns it. Used by
// requireQuizOwner so a wrong-owner attempt surfaces a clear "not your
// quiz, ask <name> to make the change" instead of a generic 403.
func render403(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager, msg string) {
	render := &TemplateRenderer{logger: logger, csrf: csrfMgr, t: parseTemplate("admin/errors/403.gohtml")}
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Forbidden",
		Message: msg,
	}
	render.Render(w, r, http.StatusForbidden, data)
}

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager) {
	render := &TemplateRenderer{logger: logger, csrf: csrfMgr, t: parseTemplate("admin/errors/500.gohtml")}
	render.Render(w, r, http.StatusInternalServerError, nil)
}

// requireQuizOwner loads the quiz with the given ID and gates the
// request on the session player being the creator. Returns the loaded
// quiz (saving the caller a second fetch) and true on success; writes
// a 403 page and returns false when the session player is not the
// creator.
//
// CreatedByPlayerID is NOT NULL at the DB level (#281, migration
// 20260520200000) and existing rows were backfilled to the lowest-id
// admin, so there is no "legacy quiz" bypass — every quiz has a real
// owner.
//
// Render-style errors (quiz missing, store failure, session missing)
// surface as 404 / 500 via the existing render helpers; this helper
// reads as the single mutating-route gate so individual handlers stay
// short.
func requireQuizOwner(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	qz, ok := quizByID(w, r, logger, csrfMgr, quizStore, id)
	if !ok {
		return nil, false
	}

	if _, present := auth.PlayerFromContext(r.Context()); !present {
		logger.ErrorContext(r.Context(), "no player on context for owner-gated route")
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	if canEditQuiz(r, qz.CreatedByPlayerID) {
		return qz, true
	}

	owner := qz.CreatedByUsername
	if owner == "" {
		owner = "another admin"
	}
	render403(w, r, logger, csrfMgr, fmt.Sprintf(
		"Only %s can edit \"%s\". Ask them to make the change, or have them transfer ownership.",
		owner, qz.Title,
	))

	return nil, false
}

// quizByID returns the quiz with the given ID from the store. It includes the questions.
// It logs any errors that occur, renders the errorpage and returns false.
func quizByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	q, err := quizStore.GetQuiz(r.Context(), id)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) || errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.ErrorContext(r.Context(), "quiz not found", slog.Any("err", err))
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching data", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return q, true
}

// questionByID returns the question with the given ID from the store.
// It logs any errors that occur, renders the errorpage and returns false.
func questionByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	questionID int64,
) (*quiz.Question, bool) {
	qs, err := quizStore.GetQuestion(r.Context(), questionID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.ErrorContext(
				r.Context(),
				fmt.Sprintf("question with ID %d not found", questionID),
				slog.Any("err", err),
			)
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(
			r.Context(),
			fmt.Sprintf("error fetching data for question with ID %d", questionID),
			slog.Any("err", err),
		)
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return qs, true
}

// fillQuizFromForm fills the quiz fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the quiz was filled successfully.
func fillQuizFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qz *quiz.Quiz,
) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return false
	}
	qz.Title = r.PostFormValue("title")
	qz.Slug = slug.Make(qz.Title)
	qz.Description = r.PostFormValue("description")
	if problems := qz.Valid(r.Context()); len(problems) > 0 {
		msg := fmt.Sprintf("validation errors: %v", problems)
		logger.ErrorContext(r.Context(), msg)
		render400(w, r, logger, csrfMgr, msg)

		return false
	}

	return true
}

// fillQuestionFromForm fills the question fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the question was filled successfully.
func fillQuestionFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qs *quiz.Question,
) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return false
	}

	qs.Text = r.PostFormValue("text")
	qs.ImageURL = r.PostFormValue("image_url")

	newOptions := make([]*quiz.Option, 0, maxOptions)

	for i := range maxOptions {
		var op *quiz.Option
		if i < len(qs.Options) {
			op = qs.Options[i]
		} else {
			op = &quiz.Option{
				QuestionID: qs.ID,
			}
		}
		if r.PostForm.Has(fmt.Sprintf("option[%d].text", i)) {
			op.ID, err = handlers.IDFromString(r.PostFormValue(fmt.Sprintf("option[%d].id", i)))
			if err != nil {
				msg := "error parsing optionID"
				logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
				render400(w, r, logger, csrfMgr, msg)

				return false
			}
			op.Text = r.PostFormValue(fmt.Sprintf("option[%d].text", i))
			op.Correct = r.PostFormValue(fmt.Sprintf("option[%d].correct", i)) == "on"

			newOptions = append(newOptions, op)
		}
	}
	qs.Options = newOptions

	if problems := qs.Valid(r.Context()); len(problems) > 0 {
		msg := fmt.Sprintf("validation errors: %v", problems)
		logger.ErrorContext(r.Context(), msg)
		render400(w, r, logger, csrfMgr, msg)

		return false
	}

	return true
}

func storeQuiz(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	qz *quiz.Quiz,
) bool {
	var err error
	if qz.ID == 0 {
		if err = quizStore.CreateQuiz(r.Context(), qz); err != nil {
			logger.ErrorContext(r.Context(), "error creating quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	} else {
		if err = quizStore.UpdateQuiz(r.Context(), qz); err != nil {
			logger.ErrorContext(r.Context(), "error updating quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	}

	return true
}

// storeQuestion creates or updates a question in the store.
func storeQuestion(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	qs *quiz.Question,
) bool {
	var err error
	if qs.ID == 0 {
		err = quizStore.CreateQuestion(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error creating question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	} else {
		err = quizStore.UpdateQuestion(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error updating question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	}

	return true
}

// HandleIndex returns the index page.
func HandleIndex(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/index.gohtml")

	type indexData struct {
		Title string
	}

	data := indexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuizList returns the quiz list page.
func HandleQuizList(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizlist.gohtml")

	type quizListData struct {
		Title   string
		Quizzes []*QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving quizzes from store", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// Counts come from a separate aggregate query so the Quiz domain
		// type doesn't have to carry a list-only field. A quiz with no
		// questions is absent from the map; the lookup yields 0.
		// A question added or deleted between this call and ListQuizzes
		// above can produce a count that's off by one for a single render
		// — acceptable for a read view; eventual consistency is fine.
		counts, err := quizStore.QuestionCountsByQuiz(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving question counts from store", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		qzd := quizDataFromQuizzes(quizzes)
		for _, qd := range qzd {
			qd.QuestionCount = counts[qd.ID]
			attachCanEdit(r, qd)
		}

		data := quizListData{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
		}

		render.Render(w, r, http.StatusOK, data)
	})
}

// PlayerScoreData represents one row of the "Played by" table on the quiz
// view page: a player who has at least one answer recorded for the quiz,
// alongside their accumulated score (computed by the game service in the
// same way the public leaderboard computes its scores).
type PlayerScoreData struct {
	PlayerID int64
	Username string
	Score    int
}

// HandleQuizView returns the quiz view page. It also fetches the per-quiz
// leaderboard so the admin can see who has played and reset their attempt
// from the same screen. We reuse the leaderboard service with a high limit
// rather than spinning up a dedicated "list participants" service method —
// see #145 for the rationale (and #141 for the performance ceilings).
func HandleQuizView(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, gameService *game.Service,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	type quizViewData struct {
		Title             string
		Quiz              *QuizData
		Players           []PlayerScoreData
		LastQuestionIndex int // len(.Quiz.Questions) - 1; used by the
		// template to disable the Down button on the last row. Sized
		// here because html/template lacks a `sub` builtin.
	}

	// playersLimit is the upper bound on rows in the "Played by" section.
	// Set high enough that real-world quiz playthroughs fit; #141 covers
	// pagination for genuinely large rosters.
	const playersLimit = 1000

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var id int64
		if id, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, id); !ok {
			return
		}

		// Admin "Played by" doesn't highlight a current player — the
		// template ignores IsCurrentPlayer — so pass 0 to flag nothing,
		// per Service.GetQuizLeaderboard's documented sentinel. The
		// admin view also has no concept of "viewer score" so the
		// CurrentPlayer field of the result is irrelevant here.
		result, err := gameService.GetQuizLeaderboard(r.Context(), id, 0, playersLimit)
		if err != nil {
			logger.ErrorContext(r.Context(), "error fetching players for quiz view", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		players := make([]PlayerScoreData, 0, len(result.Entries))
		for _, e := range result.Entries {
			players = append(players, PlayerScoreData{
				PlayerID: e.PlayerID,
				Username: e.Username,
				Score:    e.Score,
			})
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)
		data := quizViewData{
			Title:             "Admin Dashboard - View Quiz",
			Quiz:              quizData,
			Players:           players,
			LastQuestionIndex: len(quizData.Questions) - 1,
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleResetGameForPlayer hard-deletes the games (and dependent rows) that
// the given player has on the given quiz. Idempotent: if the player has no
// games, it is a 303-redirect no-op. The admin reset button on the quiz
// view page POSTs here.
func HandleResetGameForPlayer(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, gameService *game.Service,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate (#281): only the quiz's creator can reset another
		// player's attempt on it. Same rule as every other mutating
		// admin route.
		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var playerID int64
		if playerID, ok = handlers.ParseIDFromPath(w, r, logger, "playerID"); !ok {
			return
		}

		if err := gameService.ResetGamesForPlayerOnQuiz(r.Context(), playerID, quizID); err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error resetting games for player on quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// quizID came from ParseIDFromPath, which only returns an int64
		// once the path value parses cleanly — formatting it back via
		// strconv.FormatInt avoids gosec's open-redirect taint heuristic
		// for fmt.Sprintf with a path argument.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuizCreate creates a quiz.
func HandleQuizCreate(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	type quizCreateData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := quizCreateData{
			Title: "Admin Dashboard - Create Quiz",
			Quiz:  &QuizData{},
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuizEdit handles the display of the quiz edit page in the admin dashboard.
func HandleQuizEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	type quizEditData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate on the edit form itself so non-owners get a 403
		// up front instead of opening an editor they can't submit.
		var qz *quiz.Quiz
		if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		data := quizEditData{
			Title: "Admin Dashboard - Edit Quiz",
			Quiz:  quizDataFromQuiz(qz),
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// quizImportPayload mirrors the JSON shape an admin pastes into the import
// textarea. Decoupled from quiz.Quiz so the wire shape stays small and
// LLM-friendly (no IDs, timestamps, position fields, or slugs — the slug
// is derived server-side from the title). The handler translates this
// into the full domain model before validation.
type quizImportPayload struct {
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Questions   []quizImportQuestionPayload `json:"questions"`
}

type quizImportQuestionPayload struct {
	Text     string                    `json:"text"`
	ImageURL string                    `json:"imageUrl,omitempty"`
	Options  []quizImportOptionPayload `json:"options"`
}

type quizImportOptionPayload struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

// quizImportExample is the JSON block rendered on the import page so the
// admin can copy it into a chat with Claude (or any LLM), have it generate
// a quiz, and paste the result back. Kept here as a const string rather
// than in the template so the rendered example stays byte-identical to
// what the handler will actually accept.
const quizImportExample = `{
  "title": "European Capitals",
  "description": "Twelve quick-fire questions covering EU capitals.",
  "questions": [
    {
      "text": "Which city sits on the river Vltava?",
      "options": [
        { "text": "Bratislava", "correct": false },
        { "text": "Budapest",  "correct": false },
        { "text": "Prague",    "correct": true  },
        { "text": "Warsaw",    "correct": false }
      ]
    }
  ]
}`

// quizImportPageData is the render-time data for quizimport.gohtml. Both
// the form (GET) and save (POST) handlers populate it, so the type is
// declared once at package scope rather than re-declared per handler.
type quizImportPageData struct {
	Title   string
	JSON    string
	Example string
	Error   string
}

// HandleQuizImportForm renders the JSON-import page. The textarea is empty
// on a fresh GET; the POST handler re-renders this template with the
// submitted JSON intact when validation fails.
func HandleQuizImportForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.Render(w, r, http.StatusOK, quizImportPageData{
			Title:   "Admin Dashboard - Import Quiz",
			Example: quizImportExample,
		})
	})
}

// HandleQuizImportSave parses the JSON pasted into the import form, builds
// a fresh quiz.Quiz from it, and persists via the existing store path so
// the resulting row is indistinguishable from one created via the regular
// quiz form. Validation errors re-render the form with the submitted JSON
// preserved so the admin can fix the payload without re-pasting.
func HandleQuizImportSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	renderErr := func(w http.ResponseWriter, r *http.Request, jsonText, msg string) {
		render.Render(w, r, http.StatusBadRequest, quizImportPageData{
			Title:   "Admin Dashboard - Import Quiz",
			JSON:    jsonText,
			Example: quizImportExample,
			Error:   msg,
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing import form", slog.Any("err", err))
			renderErr(w, r, "", "request body too large or malformed")

			return
		}

		jsonText := r.PostFormValue("json")
		if jsonText == "" {
			renderErr(w, r, "", "json field is required")

			return
		}

		var payload quizImportPayload
		dec := json.NewDecoder(strings.NewReader(jsonText))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&payload); err != nil {
			renderErr(w, r, jsonText, fmt.Sprintf("invalid JSON: %v", err))

			return
		}

		qz := quizFromImportPayload(payload)
		if problems := qz.Valid(r.Context()); len(problems) > 0 {
			renderErr(w, r, jsonText, fmt.Sprintf("validation errors: %v", problems))

			return
		}

		// Stamp the session admin as the creator so the downstream
		// owner-gated mutating routes can match (#281).
		if p, present := auth.PlayerFromContext(r.Context()); present {
			qz.CreatedByPlayerID = p.ID
		}

		if !storeQuiz(w, r, logger, csrfMgr, quizStore, qz) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}

// quizFromImportPayload converts the wire-shape payload into the domain
// model. The slug is always derived from the title — the payload doesn't
// carry one because LLMs are bad at picking a stable slug and the
// admin form does the same derivation. Positions are assigned 1..N in
// the order questions appear in the JSON.
func quizFromImportPayload(p quizImportPayload) *quiz.Quiz {
	qz := &quiz.Quiz{
		Title:       p.Title,
		Slug:        slug.Make(p.Title),
		Description: p.Description,
	}

	qz.Questions = make([]*quiz.Question, 0, len(p.Questions))
	for i, qIn := range p.Questions {
		qs := &quiz.Question{
			Text:     qIn.Text,
			ImageURL: qIn.ImageURL,
			Position: i + 1,
		}
		qs.Options = make([]*quiz.Option, 0, len(qIn.Options))
		for _, oIn := range qIn.Options {
			qs.Options = append(qs.Options, &quiz.Option{
				Text:    oIn.Text,
				Correct: oIn.Correct,
			})
		}
		qz.Questions = append(qz.Questions, qs)
	}

	return qz
}

// HandleQuizSave saves the quiz to the database.
func HandleQuizSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		newQuiz := quizID == 0
		var qz *quiz.Quiz
		if newQuiz {
			// CREATE: stamp the session admin as the creator so the
			// owner-gated mutating routes downstream can match (#281).
			qz = &quiz.Quiz{}
			if p, present := auth.PlayerFromContext(r.Context()); present {
				qz.CreatedByPlayerID = p.ID
			}
		} else {
			// UPDATE: only the creator may save. requireQuizOwner
			// loads the quiz and 403s anyone else (#281).
			if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
				return
			}
		}

		if !fillQuizFromForm(w, r, logger, csrfMgr, qz) {
			return
		}

		if !storeQuiz(w, r, logger, csrfMgr, quizStore, qz) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}

// HandleQuestionCreate creates a question.
func HandleQuestionCreate(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	type questionCreateData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate on the question-create form: non-owners 403
		// instead of seeing a form whose POST would fail anyway.
		var qz *quiz.Quiz
		if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		data := questionCreateData{
			Title:    "Admin Dashboard - Question Create",
			Quiz:     quizDataFromQuiz(qz),
			Question: &QuestionData{},
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuestionEdit handles the display of the question edit page in the admin dashboard.
func HandleQuestionEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	type questionEditData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}
		newQuestion := questionID == 0

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		var qs *quiz.Question

		if newQuestion {
			qs = &quiz.Question{
				QuizID: quizID,
			}
		} else {
			qs, ok = questionByID(w, r, logger, csrfMgr, quizStore, questionID)
			if !ok {
				return
			}
		}

		data := questionEditData{
			Title:    "Admin Dashboard - Question Edit",
			Quiz:     quizDataFromQuiz(qz),
			Question: questionDataFromQuestion(qs),
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuizDelete deletes a quiz and all its questions and options.
func HandleQuizDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		if err := quizStore.DeleteQuiz(r.Context(), quizID); err != nil {
			if errors.Is(err, quiz.ErrDeletingQuizNoRowsAffected) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error deleting quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
	})
}

// renderQuestionMoveError translates a SwapQuestionPositions failure
// into the right HTTP response. Pulled out of HandleQuestionMove so the
// cognitive complexity of the handler stays under the revive limit
// after the owner gate was added in #281.
//
// htmxResponder is true when the caller is an HX-Request fragment swap
// — boundary errors return 204 in that mode so the existing DOM stays
// in place. Classic form posts redirect back to the quiz view; the
// rerendered page reflects the (unchanged) order from the database.
//
// Uses [strconv.FormatInt] for the redirect path to dodge gosec's
// open-redirect taint heuristic (G710) — same dance as
// HandleQuestionSave.
//
//nolint:revive // htmxResponder is a wire-format selector, not a flag-as-mode toggle; splitting the function in two would duplicate the switch rather than clarify it.
func renderQuestionMoveError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizID int64,
	err error,
	htmxResponder bool,
) {
	switch {
	case errors.Is(err, quiz.ErrInvalidDirection):
		render400(w, r, logger, csrfMgr, "invalid direction")
	case errors.Is(err, quiz.ErrQuestionAtTop),
		errors.Is(err, quiz.ErrQuestionAtBottom):
		// Boundary case: the button should have been disabled in
		// the UI, so a request here is unusual but harmless. For
		// HTMX, 204 leaves the existing DOM untouched; for the
		// classic form post, redirect back to the view.
		if htmxResponder {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
		}
	case errors.Is(err, quiz.ErrQuestionNotFound):
		render404(w, r, logger, csrfMgr)
	default:
		logger.ErrorContext(r.Context(), "error swapping question positions", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)
	}
}

// HandleQuestionMove handles the per-row Up/Down reorder buttons on the
// quiz view (#16). The {direction} path segment must be "up" or "down";
// the underlying store handles the swap atomically and returns sentinel
// errors for boundary conditions (already at top/bottom) which we map
// to 400 here so the operator sees the cause rather than a generic
// 500. After a successful swap we redirect back to the quiz view; the
// re-rendered page reflects the new order from the database.
func HandleQuestionMove(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	// The HX-Request path renders only the questions_list partial. Reuse
	// the quiz-view template tree because parseTemplate loads every
	// admin/partials/*.gohtml alongside any page template, so the partial
	// is in scope for ExecuteTemplate by name.
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	type partialData struct {
		Quiz              *QuizData
		LastQuestionIndex int
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		direction := r.PathValue("direction")
		// HTMX wire header is HX-Request; Hx-Request is Go's canonical form.
		isHX := r.Header.Get("Hx-Request") == "true"

		if err := quizStore.SwapQuestionPositions(r.Context(), quizID, questionID, direction); err != nil {
			renderQuestionMoveError(w, r, logger, csrfMgr, quizID, err, isHX)

			return
		}

		if isHX {
			// Refetch with the new order and render only the question
			// list. The whole-page render keeps happening on a hard
			// refresh; this branch just spares the round-trip.
			qz, fetchOK := quizByID(w, r, logger, csrfMgr, quizStore, quizID)
			if !fetchOK {
				return
			}
			quizData := quizDataFromQuiz(qz)
			attachCanEdit(r, quizData)
			render.RenderPartial(w, r, "questions_list", partialData{
				Quiz:              quizData,
				LastQuestionIndex: len(quizData.Questions) - 1,
			})

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuestionDelete deletes a question and all its options.
func HandleQuestionDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		if err := quizStore.DeleteQuestion(r.Context(), questionID); err != nil {
			if errors.Is(err, quiz.ErrDeletingQuestionNoRowsAffected) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error deleting question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuestionSave saves a question.
func HandleQuestionSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse quiz and question IDs from the URL
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		newQuestion := questionID == 0

		// Owner gate (#281) before loading the question and writing.
		// requireQuizOwner returns the loaded quiz, so the subsequent
		// question handling can reuse it without a second fetch.
		var qz *quiz.Quiz
		if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var qs *quiz.Question
		if newQuestion {
			qs = &quiz.Question{
				QuizID: qz.ID,
			}
		} else {
			if qs, ok = questionByID(w, r, logger, csrfMgr, quizStore, questionID); !ok {
				return
			}
		}

		if !fillQuestionFromForm(w, r, logger, csrfMgr, qs) {
			return
		}

		// Auto-assign position for new questions so authors do not have
		// to type integers (#16). Called after form validation so form
		// errors surface as 400 before this hits the store.
		if newQuestion {
			nextPos, posErr := quizStore.NextQuestionPosition(r.Context(), qz.ID)
			if posErr != nil {
				logger.ErrorContext(r.Context(), "error fetching next question position", slog.Any("err", posErr))
				render500(w, r, logger, csrfMgr)

				return
			}
			qs.Position = nextPos
		}

		if !storeQuestion(w, r, logger, csrfMgr, quizStore, qs) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}
