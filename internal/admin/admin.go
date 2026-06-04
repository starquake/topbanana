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

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/version"
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
// time ExecuteTemplate runs - an error page is no longer an option, so
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
// nonce cookie is a header write - callers must defer their own header
// writes until after prepare returns.
func (tr *TemplateRenderer) prepare(w http.ResponseWriter, r *http.Request) (*template.Template, bool) {
	t, err := tr.t.Clone()
	if err != nil {
		tr.logger.ErrorContext(r.Context(), "error cloning template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	displayName := ""
	isAdmin := false
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		displayName = p.DisplayName
		isAdmin = p.IsAdmin()
	}

	csrfToken := ""
	if tr.csrf != nil {
		csrfToken = tr.csrf.Token(w, r)
	}

	section := navSection(r.URL.Path)

	return t.Funcs(template.FuncMap{
		"currentUser": func() string { return displayName },
		"csrfToken":   func() string { return csrfToken },
		"ogImage":     func() string { return absurl.BaseURL(r) + "/assets/og-image.png" },
		"navSection":  func() string { return section },
		"isAdmin":     func() bool { return isAdmin },
	}), true
}

// navSection maps a request path to the admin nav section it belongs to,
// so the navbar can mark the active section. The empty string means the
// overview at /admin (no inline link is active).
func navSection(path string) string {
	switch {
	case strings.HasPrefix(path, "/admin/quizzes"):
		return "quizzes"
	case strings.HasPrefix(path, "/admin/players"):
		return "players"
	case strings.HasPrefix(path, "/admin/invites"):
		return "invites"
	case strings.HasPrefix(path, "/admin/email"):
		return "email"
	case strings.HasPrefix(path, "/admin/settings"):
		return "settings"
	default:
		return ""
	}
}

// QuizData is the data for the quiz list page, it shows multiple
// quizzes when available. CanEdit is the resolved
// "current-session-admin == creator" decision so the templates and
// the questions_list partial do not have to recompute the rule (#281)
// - handlers populate it via [attachCanEdit] before rendering, and a
// rule change lives entirely in Go.
type QuizData struct {
	ID                   int64
	Title                string
	Slug                 string
	Description          string
	UpdatedAt            time.Time
	QuestionCount        int
	CreatedByPlayerID    int64
	CreatedByDisplayName string
	CanEdit              bool
	TimeLimitSeconds     int
	Visibility           string
	// VisibilityOptions feeds the admin form's selector - pulled
	// straight from the domain constants so a future level addition
	// only touches one place.
	VisibilityOptions []string
	Questions         []*QuestionData
}

// QuestionData is the data for a question. TimeLimitSecondsValue is the
// pre-formatted value bound to the optional per-question time-limit
// input - empty when the question inherits the quiz default (#99), so
// the form's <input type="number"> stays blank rather than rendering 0.
type QuestionData struct {
	ID                    int64
	QuizID                int64
	RoundID               int64
	Text                  string
	ImageURL              string
	Position              int
	TimeLimitSecondsValue string
	Options               []*OptionData
}

// RoundData backs the round sections on the quiz view and the round
// form. Mirrors the QuestionData/QuizData shape so the templates stay
// symmetric with their question equivalents (#444).
type RoundData struct {
	ID       int64
	QuizID   int64
	Title    string
	Summary  string
	Position int
}

func roundDataFromRound(r *quiz.Round) *RoundData {
	return &RoundData{
		ID:       r.ID,
		QuizID:   r.QuizID,
		Title:    r.Title,
		Summary:  r.Summary,
		Position: r.Position,
	}
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

// canEditQuiz is the single source of truth for the creator-or-Admin edit rule
// (#281/#538): the session player must be present and must either be the quiz's
// creator OR an Admin (who may edit, delete, and reset scores on any quiz). A
// Host is NOT granted rights over another Host's games - own-game checks still
// go through createdByPlayerID. Both [attachCanEdit] (read paths) and
// [requireQuizOwner] (mutating paths) call this so the policy lives in one
// place.
func canEditQuiz(r *http.Request, createdByPlayerID int64) bool {
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		return false
	}

	return p.IsAdmin() || p.ID == createdByPlayerID
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
	visibility := qz.Visibility
	if visibility == "" {
		visibility = quiz.VisibilityPublic
	}

	return &QuizData{
		ID:                   qz.ID,
		Title:                qz.Title,
		Slug:                 qz.Slug,
		Description:          qz.Description,
		UpdatedAt:            qz.UpdatedAt,
		QuestionCount:        len(qz.Questions),
		CreatedByPlayerID:    qz.CreatedByPlayerID,
		CreatedByDisplayName: qz.CreatedByDisplayName,
		TimeLimitSeconds:     qz.TimeLimitSeconds,
		Visibility:           visibility,
		VisibilityOptions:    quiz.VisibilityValues(),
		Questions:            questionDataFromQuestions(qz.Questions),
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
	timeLimit := ""
	if q.TimeLimitSeconds != nil {
		timeLimit = strconv.Itoa(*q.TimeLimitSeconds)
	}

	return &QuestionData{
		ID:                    q.ID,
		QuizID:                q.QuizID,
		RoundID:               q.RoundID,
		Text:                  q.Text,
		ImageURL:              q.ImageURL,
		Position:              q.Position,
		TimeLimitSecondsValue: timeLimit,
		Options:               optionDataFromOptions(q.Options),
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
// Placeholder "currentUser", "csrfToken", and "navSection" funcs are
// registered before parse so the navbar's {{currentUser}}/{{navSection}}
// calls and any form's {{csrfToken}} call resolve at parse time.
// TemplateRenderer.Render clones the parsed tree and replaces these
// placeholders with implementations that read the request context, CSRF
// manager, and request path, respectively.
//
// "humanizeTime" is a pure function of its argument, so it's registered with
// its real implementation here - no per-request override needed.
func parseTemplate(path string) *template.Template {
	funcs := template.FuncMap{
		"currentUser":       func() string { return "" },
		"csrfToken":         func() string { return "" },
		"ogImage":           func() string { return "" },
		"navSection":        func() string { return "" },
		"isAdmin":           func() bool { return false },
		"envTitleTag":       envtag.Get,
		"versionLabel":      version.Label,
		"humanizeTime":      humanizeTime,
		"passwordMinLength": func() int { return auth.MinPasswordLength },
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
// hoc deep in the call stack - passing it explicitly keeps the rendering path
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

// requireQuizOwner loads the quiz and gates the request on the session
// player being its creator. Returns the loaded quiz on success;
// renders 403 / 404 / 500 on the failure paths.
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

	// RequireAdmin (auth/middleware.go) already enforces a populated
	// player on the context before any admin handler runs, and
	// canEditQuiz below handles the not-present case correctly. The
	// previous explicit check rendered 500 on a state that's
	// unreachable under the production wiring (#371).
	if canEditQuiz(r, qz.CreatedByPlayerID) {
		return qz, true
	}

	owner := qz.CreatedByDisplayName
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
			// User-supplied bad ID (or stale link after delete) - Info,
			// not Error (#369).
			logger.InfoContext(r.Context(), "quiz not found", slog.Any("err", err))
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching data", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return q, true
}

// questionByID loads the question with the given ID and verifies it
// belongs to the supplied quizID. A mismatch renders as 404 (not 403)
// so the route never leaks "this question exists on another quiz"
// - the IDOR fix for #339 lives here: every mutating question route
// is quiz-scoped in the URL, so loading by questionID alone would let
// an admin who owns quizA edit a question on quizB by mounting it as
// /admin/quizzes/A/questions/B-question. SwapQuestionPositions does
// its own quiz-scoping; the read + write + delete paths route through
// this helper.
func questionByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID, questionID int64,
) (*quiz.Question, bool) {
	qs, err := quizStore.GetQuestion(r.Context(), questionID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.InfoContext(
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

	if qs.QuizID != quizID {
		logger.InfoContext(
			r.Context(),
			fmt.Sprintf("question %d belongs to quiz %d, not URL-scoped quiz %d", questionID, qs.QuizID, quizID),
		)
		render404(w, r, logger, csrfMgr)

		return nil, false
	}

	return qs, true
}

// fillQuizFromForm fills the quiz fields from the form values.
// On a parse error it renders a 400 page directly and returns
// (nil, false); the caller should just return. On a validation error
// it leaves the fields populated on qz so the caller can re-render the
// form, and returns (fieldErrors, true) with a non-empty map keyed by
// lowercased form-field name (title, description). On success it
// returns (nil, true).
func fillQuizFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qz *quiz.Quiz,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}
	qz.Title = r.PostFormValue("title")
	qz.Slug = slug.Make(qz.Title)
	qz.Description = r.PostFormValue("description")
	// Per-quiz default time limit (#99). Empty input falls back to the
	// migration default so a host that never touched the field still
	// gets the historical 10-second window; an unparseable value lands
	// 0, which the Quiz.Valid range check rejects with an inline error.
	raw := strings.TrimSpace(r.PostFormValue("time_limit_seconds"))
	switch raw {
	case "":
		qz.TimeLimitSeconds = quiz.DefaultTimeLimitSeconds
	default:
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			n = 0
		}
		qz.TimeLimitSeconds = n
	}
	// Visibility input (#103). Defaults to public if the form omits it
	// (older admin clients or curl probes); an unrecognised value is
	// passed through verbatim so Quiz.Valid surfaces an inline error.
	if v := r.PostFormValue("visibility"); v != "" {
		qz.Visibility = v
	} else {
		qz.Visibility = quiz.VisibilityPublic
	}
	if problems := (&quizForm{quiz: qz}).Valid(r.Context()); len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

// parseOptionalTimeLimit interprets the optional per-question
// time_limit_seconds input. Blank -> nil (inherit the quiz default).
// Garbage -> a non-nil pointer to 0, which Question.Valid catches and
// surfaces as an inline range error.
func parseOptionalTimeLimit(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		n = 0
	}

	return &n
}

// fillQuestionFromForm fills the question fields from the form values.
// On a parse error it renders a 400 page directly and returns
// (nil, false); the caller should just return. On a validation error
// it leaves the fields populated on qs so the caller can re-render the
// form, and returns (fieldErrors, true) with a non-empty map keyed by
// lowercased form-field name (text, options). On success it returns
// (nil, true).
func fillQuestionFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qs *quiz.Question,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}

	qs.Text = r.PostFormValue("text")
	qs.ImageURL = r.PostFormValue("image_url")
	// Optional per-question override (#99). Blank input clears any
	// previous override (NULL -> inherit the quiz default); a parse
	// failure lands a zero, which Question.Valid rejects with an
	// inline range error rather than silently saving a bad value.
	qs.TimeLimitSeconds = parseOptionalTimeLimit(r.PostFormValue("time_limit_seconds"))

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

				return nil, false
			}
			op.Text = r.PostFormValue(fmt.Sprintf("option[%d].text", i))
			op.Correct = r.PostFormValue(fmt.Sprintf("option[%d].correct", i)) == "on"

			newOptions = append(newOptions, op)
		}
	}
	qs.Options = newOptions

	if problems := (&questionForm{question: qs}).Valid(r.Context()); len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

// storeQuiz persists qz via the appropriate Create/Update path. It does
// no rendering; callers branch on the returned error so they can pick
// the right user-facing response - in particular [quiz.ErrSlugTaken],
// which both HandleQuizSave and HandleQuizImportSave translate into a
// 409 + form re-render with an inline message (#293) rather than the
// generic 500 the wrapped SQL error used to produce.
func storeQuiz(ctx context.Context, quizStore quiz.Store, qz *quiz.Quiz) error {
	if qz.ID == 0 {
		if err := quizStore.CreateQuiz(ctx, qz); err != nil {
			return fmt.Errorf("create quiz: %w", err)
		}

		return nil
	}
	if err := quizStore.UpdateQuiz(ctx, qz); err != nil {
		return fmt.Errorf("update quiz: %w", err)
	}

	return nil
}

// storeQuestion creates or updates a question in the store. On a new
// question (ID == 0) it routes through CreateQuestionAtNextPosition so
// the position read + insert run inside a single transaction, killing
// the TOCTOU race that produced two questions at the same position
// under concurrent "Add question" clicks (#352).
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
		err = quizStore.CreateQuestionAtNextPosition(r.Context(), qs)
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
		// - acceptable for a read view; eventual consistency is fine.
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
// view page: a player who has finished every quiz question, alongside
// their accumulated score (computed by the game service in the same way
// the public leaderboard computes its scores). HandleQuizView filters out
// in-progress and pre-answer participants (#244/#335) so the admin's
// Reset button is only offered for games the host can safely wipe.
type PlayerScoreData struct {
	PlayerID    int64
	DisplayName string
	Score       int
}

// HandleQuizView returns the quiz view page. It also fetches the per-quiz
// leaderboard so the admin can see who has played and reset their attempt
// from the same screen. We reuse the leaderboard service with a high limit
// rather than spinning up a dedicated "list participants" service method -
// see #145 for the rationale (and #141 for the performance ceilings).
func HandleQuizView(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, gameService *game.Service,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

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

		players, ok := loadCompletedPlayers(w, r, logger, csrfMgr, gameService, id)
		if !ok {
			return
		}

		rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, id)
		if !ok {
			return
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)
		data := newQuizViewData(quizData, players, rounds)
		render.Render(w, r, http.StatusOK, data)
	})
}

// QuizViewData is the data passed to the quiz view template. Questions
// are grouped into rounds in play order; the template ranges over
// Rounds instead of a flat question list (#444).
type QuizViewData struct {
	Title   string
	Quiz    *QuizData
	Players []PlayerScoreData
	// LastQuestionPosition is the highest question position in the quiz;
	// the partial keys the move-down button's disabled state on it.
	LastQuestionPosition int
	// Rounds is the position-ordered round list, each carrying its own
	// questions, for the grouped quiz view.
	Rounds []RoundViewData
	// MoveTargets lists every round as a move-question-into-round option;
	// the per-question dropdown only renders when more than one exists.
	MoveTargets []RoundMoveTarget
}

// RoundViewData is one round section on the quiz view: the round itself,
// its questions in quiz-wide position order, and the per-round reorder
// flags. CanMoveUp/CanMoveDown drive arrow-button visibility - the
// store's MoveRound re-validates so the flags are UX-only, not security.
type RoundViewData struct {
	Round       *RoundData
	Questions   []*QuestionData
	CanMoveUp   bool
	CanMoveDown bool
}

// RoundMoveTarget is one entry in the move-question-into-round dropdown.
type RoundMoveTarget struct {
	ID    int64
	Title string
}

// buildRoundView groups the quiz's questions under their rounds in
// position order. Questions keep their quiz-wide position order within a
// round; a round with no questions still renders its section. Questions
// whose round_id matches no round (a defensive case) are dropped from
// the grouped view rather than duplicated.
func buildRoundView(rounds []*quiz.Round, questions []*QuestionData) []RoundViewData {
	byRound := make(map[int64][]*QuestionData, len(rounds))
	for _, q := range questions {
		byRound[q.RoundID] = append(byRound[q.RoundID], q)
	}

	views := make([]RoundViewData, 0, len(rounds))
	for i, rnd := range rounds {
		views = append(views, RoundViewData{
			Round:       roundDataFromRound(rnd),
			Questions:   byRound[rnd.ID],
			CanMoveUp:   i > 0,
			CanMoveDown: i < len(rounds)-1,
		})
	}

	return views
}

// roundMoveTargets maps the quiz's rounds to dropdown entries.
func roundMoveTargets(rounds []*quiz.Round) []RoundMoveTarget {
	targets := make([]RoundMoveTarget, 0, len(rounds))
	for _, rnd := range rounds {
		targets = append(targets, RoundMoveTarget{ID: rnd.ID, Title: rnd.Title})
	}

	return targets
}

// lastQuestionPosition returns the highest position among the quiz's
// questions, or 0 when the quiz has none. Questions are stored in
// ascending position order, so the last entry carries the max.
func lastQuestionPosition(questions []*QuestionData) int {
	if len(questions) == 0 {
		return 0
	}

	return questions[len(questions)-1].Position
}

// loadRounds fetches the quiz's rounds in position order. Errors are
// 500s because the section is part of the same admin view that already
// loaded the quiz tree; surfacing an empty list would hide the
// failure.
func loadRounds(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID int64,
) ([]*quiz.Round, bool) {
	rounds, err := quizStore.ListRoundsByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing rounds for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return rounds, true
}

func newQuizViewData(quizData *QuizData, players []PlayerScoreData, rounds []*quiz.Round) QuizViewData {
	return QuizViewData{
		Title:                "Admin Dashboard - View Quiz",
		Quiz:                 quizData,
		Players:              players,
		LastQuestionPosition: lastQuestionPosition(quizData.Questions),
		Rounds:               buildRoundView(rounds, quizData.Questions),
		MoveTargets:          roundMoveTargets(rounds),
	}
}

// roundsPartialData mirrors the subset of QuizViewData the
// questions_list partial actually ranges over. Shared by the question
// and round move handlers so an HTMX swap keeps the page's scroll
// position instead of bouncing through a 303.
type roundsPartialData struct {
	Quiz                 *QuizData
	LastQuestionPosition int
	Rounds               []RoundViewData
	MoveTargets          []RoundMoveTarget
}

// renderRoundsPartial refetches the quiz tree and emits the
// questions_list partial. Used by the HTMX paths of HandleQuestionMove
// and HandleRoundMove so a successful (or knowingly-impossible) move
// updates only the grouped block instead of a full page reload.
func renderRoundsPartial(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	render *TemplateRenderer,
	quizStore quiz.Store,
	quizID int64,
) {
	qz, ok := quizByID(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return
	}
	rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return
	}
	quizData := quizDataFromQuiz(qz)
	attachCanEdit(r, quizData)
	render.RenderPartial(w, r, "questions_list", roundsPartialData{
		Quiz:                 quizData,
		LastQuestionPosition: lastQuestionPosition(quizData.Questions),
		Rounds:               buildRoundView(rounds, quizData.Questions),
		MoveTargets:          roundMoveTargets(rounds),
	})
}

// quizViewPlayersLimit is the upper bound on rows in the "Played by"
// section. Set high enough that real-world quiz playthroughs fit; #141
// covers pagination for genuinely large rosters.
const quizViewPlayersLimit = 1000

// loadCompletedPlayers pulls the leaderboard for the given quiz and
// returns only the entries that finished. Mid-quiz / pre-answer
// entries are skipped (#244/#335) so the admin's Reset button never
// pulls the rug from a live session. Writes a 500 page and returns
// ok=false on a service failure.
func loadCompletedPlayers(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	gameService *game.Service,
	quizID int64,
) ([]PlayerScoreData, bool) {
	// Admin "Played by" doesn't highlight a current player - the
	// template ignores IsCurrentPlayer - so pass 0 to flag nothing,
	// per Service.GetQuizLeaderboard's documented sentinel.
	result, err := gameService.GetQuizLeaderboard(r.Context(), quizID, 0, quizViewPlayersLimit)
	if err != nil {
		logger.ErrorContext(r.Context(), "error fetching players for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	players := make([]PlayerScoreData, 0, len(result.Entries))
	for _, e := range result.Entries {
		if !e.Completed {
			continue
		}
		players = append(players, PlayerScoreData{
			PlayerID:    e.PlayerID,
			DisplayName: e.DisplayName,
			Score:       e.Score,
		})
	}

	return players, true
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
		// once the path value parses cleanly - formatting it back via
		// strconv.FormatInt avoids gosec's open-redirect taint heuristic
		// for fmt.Sprintf with a path argument.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuizCreate creates a quiz.
func HandleQuizCreate(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pre-fill the time-limit input with the project-wide default
		// so the form is a valid submission without the author having
		// to touch the new field; the HTML5 number input with
		// min=1/max=600 would otherwise reject the zero-value (#99).
		render.Render(w, r, http.StatusOK, quizFormData{
			Title: quizFormCreateTitle,
			Quiz: &QuizData{
				TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
				Visibility:        quiz.VisibilityPublic,
				VisibilityOptions: quiz.VisibilityValues(),
			},
		})
	})
}

// HandleQuizEdit handles the display of the quiz edit page in the admin dashboard.
func HandleQuizEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

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
		render.Render(w, r, http.StatusOK, quizFormData{
			Title: quizFormEditTitle,
			Quiz:  quizDataFromQuiz(qz),
		})
	})
}

// quizImportPayload mirrors the JSON shape an admin pastes into the import
// textarea. Decoupled from quiz.Quiz so the wire shape stays small and
// LLM-friendly (no IDs, timestamps, position fields, or slugs - the slug
// is derived server-side from the title). The handler translates this
// into the full domain model before validation.
type quizImportPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// TimeLimitSeconds is the per-quiz default answer window (#99).
	// Optional in the payload - omitted maps to
	// [quiz.DefaultTimeLimitSeconds], matching the admin form's
	// new-quiz default.
	TimeLimitSeconds *int `json:"timeLimitSeconds,omitempty"`
	// Questions and Rounds are mutually exclusive (#546). Supply
	// Questions for a flat quiz (every question lands in the default
	// round, the original behaviour) or Rounds to author named rounds
	// with their own questions - never both, never neither.
	Questions []quizImportQuestionPayload `json:"questions,omitempty"`
	Rounds    []quizImportRoundPayload    `json:"rounds,omitempty"`
}

type quizImportRoundPayload struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	// Questions for this round, in play order. Required and non-empty;
	// quiz-wide positions are assigned 1..N across all rounds (#546).
	Questions []quizImportQuestionPayload `json:"questions"`
}

type quizImportQuestionPayload struct {
	Text     string `json:"text"`
	ImageURL string `json:"imageUrl,omitempty"`
	// TimeLimitSeconds overrides the quiz default for this question
	// (#99). Optional - omitted means "inherit the quiz value at
	// game time", same as leaving the admin form's field blank.
	TimeLimitSeconds *int                      `json:"timeLimitSeconds,omitempty"`
	Options          []quizImportOptionPayload `json:"options"`
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
  "description": "A quick tour of EU capitals.",
  "timeLimitSeconds": 10,
  "rounds": [
    {
      "title": "Warm-up",
      "summary": "An easy start before things speed up.",
      "questions": [
        {
          "text": "Which city sits on the river Vltava?",
          "options": [
            { "text": "Bratislava", "correct": false },
            { "text": "Budapest",   "correct": false },
            { "text": "Prague",     "correct": true  },
            { "text": "Warsaw",     "correct": false }
          ]
        },
        {
          "text": "Which of these is a capital city?",
          "options": [
            { "text": "Lisbon",   "correct": true  },
            { "text": "Porto",    "correct": false },
            { "text": "Helsinki", "correct": true  },
            { "text": "Tampere",  "correct": false }
          ]
        }
      ]
    },
    {
      "title": "Final stretch",
      "summary": "One harder question to finish.",
      "questions": [
        {
          "text": "Which capital is furthest north?",
          "timeLimitSeconds": 20,
          "options": [
            { "text": "Reykjavik",  "correct": true  },
            { "text": "Oslo",       "correct": false },
            { "text": "Stockholm",  "correct": false },
            { "text": "Copenhagen", "correct": false }
          ]
        }
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

	renderStatus := func(w http.ResponseWriter, r *http.Request, status int, jsonText, msg string) {
		render.Render(w, r, status, quizImportPageData{
			Title:   "Admin Dashboard - Import Quiz",
			JSON:    jsonText,
			Example: quizImportExample,
			Error:   msg,
		})
	}
	renderErr := func(w http.ResponseWriter, r *http.Request, jsonText, msg string) {
		renderStatus(w, r, http.StatusBadRequest, jsonText, msg)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parsed, ok := parseImportPayload(w, r, logger, renderErr)
		if !ok {
			return
		}

		// Stamp the session admin as the creator so the downstream
		// owner-gated mutating routes can match (#281).
		if p, present := auth.PlayerFromContext(r.Context()); present {
			parsed.Quiz.CreatedByPlayerID = p.ID
		}

		if err := storeQuiz(r.Context(), quizStore, parsed.Quiz); err != nil {
			if errors.Is(err, quiz.ErrSlugTaken) {
				// Same slug-derivation rule applies on the import path
				// (#293): re-render at 409 with the JSON intact so the
				// admin can rename and resubmit without re-pasting.
				renderStatus(
					w, r, http.StatusConflict, parsed.JSONText,
					"A quiz with this title already exists - change the title in the JSON and resubmit.",
				)

				return
			}
			logger.ErrorContext(r.Context(), "error storing imported quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", parsed.Quiz.ID), http.StatusSeeOther)
	})
}

// parsedImport holds the decoded + validated payload [parseImportPayload]
// returns to [HandleQuizImportSave]. Bundled so the parser can return a
// single struct (plus an ok flag) and stay under revive's
// function-result-limit while still surfacing the JSON text (for
// re-render on later failures) alongside the parsed quiz.
type parsedImport struct {
	JSONText string
	Quiz     *quiz.Quiz
}

// parseImportPayload reads + decodes + validates the request body for
// [HandleQuizImportSave]. On any failure it writes the form-rendered
// error response via renderErr and returns ok=false; the caller
// early-returns. Split out so [HandleQuizImportSave] stays under
// revive's function-length and gocognit limits.
func parseImportPayload(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, string, string),
) (parsedImport, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		logger.ErrorContext(r.Context(), "error parsing import form", slog.Any("err", err))
		renderErr(w, r, "", "request body too large or malformed")

		return parsedImport{}, false
	}

	jsonText := r.PostFormValue("json")
	if jsonText == "" {
		renderErr(w, r, "", "json field is required")

		return parsedImport{}, false
	}

	var payload quizImportPayload
	dec := json.NewDecoder(strings.NewReader(jsonText))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		renderErr(w, r, jsonText, fmt.Sprintf("invalid JSON: %v", err))

		return parsedImport{}, false
	}

	qz, err := quizFromImportPayload(payload)
	if err != nil {
		renderErr(w, r, jsonText, fmt.Sprintf("validation errors: %v", err))

		return parsedImport{}, false
	}
	if problems := (&quizForm{quiz: qz}).Valid(r.Context()); len(problems) > 0 {
		renderErr(w, r, jsonText, fmt.Sprintf("validation errors: %v", problems))

		return parsedImport{}, false
	}

	return parsedImport{JSONText: jsonText, Quiz: qz}, true
}

var (
	// errImportQuestionsOrRounds is returned when the payload supplies
	// both a top-level questions[] and rounds[], or neither. The two are
	// mutually exclusive (#546): one flat list or named rounds, not a mix.
	errImportQuestionsOrRounds = errors.New(
		"provide either a top-level questions array or a rounds array, not both and not neither",
	)
	// errImportRoundTitleRequired is returned when an imported round
	// carries no title (#546).
	errImportRoundTitleRequired = errors.New("title is required")
	// errImportRoundNoQuestions is returned when an imported round
	// carries no questions (#546).
	errImportRoundNoQuestions = errors.New("at least one question is required")
)

// quizFromImportPayload converts the wire-shape payload into the domain
// model. The slug is always derived from the title - the payload doesn't
// carry one because LLMs are bad at picking a stable slug and the admin
// form does the same derivation. Question positions are assigned 1..N in
// payload order across all rounds.
//
// When the payload carries rounds[], the rounds are mapped onto
// Quiz.Rounds (each with its own questions) and the same questions are
// also flattened onto Quiz.Questions so the shared quizForm.Valid runs
// every per-question rule. With a top-level questions[] instead, the
// store drops everything in the quiz's default round, the original
// behaviour (#546).
func quizFromImportPayload(p quizImportPayload) (*quiz.Quiz, error) {
	if (len(p.Questions) == 0) == (len(p.Rounds) == 0) {
		return nil, errImportQuestionsOrRounds
	}

	// #99: honour the payload's per-quiz default when present; fall
	// back to the project value so authors who don't care can omit
	// the field entirely and still pass Quiz.Valid's range check.
	timeLimit := quiz.DefaultTimeLimitSeconds
	if p.TimeLimitSeconds != nil {
		timeLimit = *p.TimeLimitSeconds
	}
	qz := &quiz.Quiz{
		Title:            p.Title,
		Slug:             slug.Make(p.Title),
		Description:      p.Description,
		TimeLimitSeconds: timeLimit,
	}

	if len(p.Rounds) > 0 {
		if err := fillQuizFromRounds(qz, p.Rounds); err != nil {
			return nil, err
		}

		return qz, nil
	}

	qz.Questions = make([]*quiz.Question, 0, len(p.Questions))
	pos := 0
	for _, qIn := range p.Questions {
		pos++
		qz.Questions = append(qz.Questions, questionFromImportPayload(qIn, pos))
	}

	return qz, nil
}

// fillQuizFromRounds maps the authored rounds onto qz.Rounds and mirrors
// every question onto qz.Questions with a quiz-wide 1..N position in
// payload order, so the shared quizForm.Valid sees the full question set
// (#546). A round must carry a non-empty title and at least one question.
func fillQuizFromRounds(qz *quiz.Quiz, rounds []quizImportRoundPayload) error {
	qz.Rounds = make([]*quiz.Round, 0, len(rounds))
	pos := 0
	for i, rIn := range rounds {
		if rIn.Title == "" {
			return fmt.Errorf("round %d: %w", i+1, errImportRoundTitleRequired)
		}
		if len(rIn.Questions) == 0 {
			return fmt.Errorf("round %q: %w", rIn.Title, errImportRoundNoQuestions)
		}

		round := &quiz.Round{
			Position:  i,
			Title:     rIn.Title,
			Summary:   rIn.Summary,
			Questions: make([]*quiz.Question, 0, len(rIn.Questions)),
		}
		for _, qIn := range rIn.Questions {
			pos++
			qs := questionFromImportPayload(qIn, pos)
			round.Questions = append(round.Questions, qs)
			qz.Questions = append(qz.Questions, qs)
		}
		qz.Rounds = append(qz.Rounds, round)
	}

	return nil
}

// questionFromImportPayload maps one import question onto the domain
// type at the given quiz-wide position.
func questionFromImportPayload(qIn quizImportQuestionPayload, position int) *quiz.Question {
	qs := &quiz.Question{
		Text:     qIn.Text,
		ImageURL: qIn.ImageURL,
		Position: position,
		// nil -> "inherit the quiz default", the same semantics
		// the admin form's blank input carries (#99).
		TimeLimitSeconds: qIn.TimeLimitSeconds,
	}
	qs.Options = make([]*quiz.Option, 0, len(qIn.Options))
	for _, oIn := range qIn.Options {
		qs.Options = append(qs.Options, &quiz.Option{
			Text:    oIn.Text,
			Correct: oIn.Correct,
		})
	}

	return qs
}

// quizFormData backs the quizform.gohtml template. Error is non-empty
// when the POST handler re-renders the form after a recoverable
// banner-level failure (currently the slug-collision 409 from #293).
// FieldErrors is non-empty when domain-level validation fails (#32) and
// surfaces the per-input message under each invalid field. Either path
// preserves the submitted Title/Description on Quiz so the admin can
// fix and retry without re-typing.
type quizFormData struct {
	Title       string
	Quiz        *QuizData
	Error       string
	FieldErrors map[string]string
}

// HandleQuizSave saves the quiz to the database.
func HandleQuizSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

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

		fieldErrors, ok := fillQuizFromForm(w, r, logger, csrfMgr, qz)
		if !ok {
			return
		}
		title := quizFormEditTitle
		if newQuiz {
			title = quizFormCreateTitle
		}
		if len(fieldErrors) > 0 {
			// Domain-level validation failed. Re-render the same form
			// at 400 with FieldErrors set; the template uses them to
			// decorate each invalid input and show the per-field
			// message. Submitted values are preserved on qz.
			formRenderer.Render(w, r, http.StatusBadRequest, quizFormData{
				Title:       title,
				Quiz:        quizDataFromQuiz(qz),
				FieldErrors: fieldErrors,
			})

			return
		}

		if err := storeQuiz(r.Context(), quizStore, qz); err != nil {
			renderQuizSaveError(w, r, logger, csrfMgr, formRenderer, qz, title, err)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}

// renderQuizSaveError handles the storeQuiz failure paths for
// HandleQuizSave. Split out so HandleQuizSave's main flow keeps a single
// happy-path return. [quiz.ErrSlugTaken] re-renders the form at 409
// with the submitted Title/Description preserved (#293); anything else
// is treated as a genuine 500. pageTitle is the rendered <title> - the
// caller picks it from quizFormCreateTitle / quizFormEditTitle based on
// whether the POST landed on create or edit.
func renderQuizSaveError(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, csrfMgr *csrf.Manager,
	formRenderer *TemplateRenderer,
	qz *quiz.Quiz, pageTitle string, err error,
) {
	if errors.Is(err, quiz.ErrSlugTaken) {
		formRenderer.Render(w, r, http.StatusConflict, quizFormData{
			Title: pageTitle,
			Quiz:  quizDataFromQuiz(qz),
			Error: "A quiz with this title already exists - pick a different title (or rename the existing quiz).",
		})

		return
	}
	logger.ErrorContext(r.Context(), "error storing quiz", slog.Any("err", err))
	render500(w, r, logger, csrfMgr)
}

// Page <title> strings for the quiz create/edit form. Exposed as
// package-level constants so the GET (HandleQuizCreate / HandleQuizEdit)
// and the slug-conflict re-render path (HandleQuizSave) share one
// source of truth - a rename has to touch both renders together (#293).
const (
	quizFormCreateTitle = "Admin Dashboard - Create Quiz"
	quizFormEditTitle   = "Admin Dashboard - Edit Quiz"
)

// questionFormData backs questionform.gohtml. FieldErrors is set when
// HandleQuestionSave re-renders the form after a domain-level
// validation failure (#32); the per-input error message lives under
// the lowercased form-field name (text, options).
type questionFormData struct {
	Title       string
	Quiz        *QuizData
	Question    *QuestionData
	FieldErrors map[string]string
}

// HandleQuestionCreate creates a question.
func HandleQuestionCreate(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

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

		render.Render(w, r, http.StatusOK, questionFormData{
			Title:    "Admin Dashboard - Question Create",
			Quiz:     quizDataFromQuiz(qz),
			Question: &QuestionData{},
		})
	})
}

// HandleQuestionEdit handles the display of the question edit page in the admin dashboard.
func HandleQuestionEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

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
			qs, ok = questionByID(w, r, logger, csrfMgr, quizStore, quizID, questionID)
			if !ok {
				return
			}
		}

		render.Render(w, r, http.StatusOK, questionFormData{
			Title:    "Admin Dashboard - Question Edit",
			Quiz:     quizDataFromQuiz(qz),
			Question: questionDataFromQuestion(qs),
		})
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
// into the right HTTP response. In HX-Request mode, boundary errors
// return 204 so the existing DOM stays in place; classic form posts
// redirect back to the quiz view.
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
			renderRoundsPartial(w, r, logger, csrfMgr, render, quizStore, quizID)

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

		// Reject cross-quiz deletes (#339); without this gate an admin
		// who owns quizID could delete a question on a different quiz
		// by mounting it on this URL.
		if _, ok = questionByID(w, r, logger, csrfMgr, quizStore, quizID, questionID); !ok {
			return
		}

		if err := quizStore.DeleteQuestion(r.Context(), questionID); err != nil {
			logger.ErrorContext(r.Context(), "error deleting question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// questionSaveCtx is the artefact set loadQuestionForSave returns -
// bundled into a struct so HandleQuestionSave's signature stays under
// revive's function-result-limit and the call site stays readable.
type questionSaveCtx struct {
	Quiz     *quiz.Quiz
	Question *quiz.Question
	IsNew    bool
}

// HandleQuestionSave saves a question.
func HandleQuestionSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qctx, ok := loadQuestionForSave(w, r, logger, csrfMgr, quizStore)
		if !ok {
			return
		}

		fieldErrors, ok := fillQuestionFromForm(w, r, logger, csrfMgr, qctx.Question)
		if !ok {
			return
		}
		if len(fieldErrors) > 0 {
			renderQuestionForm(w, r, formRenderer, qctx, fieldErrors)

			return
		}

		// New questions get their position assigned inside the store's
		// txn-wrapped CreateQuestionAtNextPosition (#352) so the
		// max+1 read can't race with a concurrent insert. The handler
		// just passes the question through; storeQuestion picks the
		// right store method based on qs.ID.
		if !storeQuestion(w, r, logger, csrfMgr, quizStore, qctx.Question) {
			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// - the qz.ID came from a request parameter through
		// requireQuizOwner so gosec flags fmt.Sprintf's %d as tainted.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(qctx.Quiz.ID, 10), http.StatusSeeOther)
	})
}

// loadQuestionForSave parses the quizID + questionID off the path,
// applies the owner gate, and loads the existing question for an edit
// (or stamps a fresh struct for a create). ok=false when any step
// failed and already wrote a response. Split out so
// HandleQuestionSave's main flow stays under gocognit's threshold
// while the participant + ownership gates remain consolidated.
//

func loadQuestionForSave(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
) (*questionSaveCtx, bool) {
	quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
	if !ok {
		return nil, false
	}
	questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
	if !ok {
		return nil, false
	}
	qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return nil, false
	}
	if questionID == 0 {
		return &questionSaveCtx{Quiz: qz, Question: &quiz.Question{QuizID: qz.ID}, IsNew: true}, true
	}
	qs, ok := questionByID(w, r, logger, csrfMgr, quizStore, qz.ID, questionID)
	if !ok {
		return nil, false
	}

	return &questionSaveCtx{Quiz: qz, Question: qs, IsNew: false}, true
}

// renderQuestionForm re-renders the question form after a validation
// failure on save. The submitted Question + FieldErrors are preserved
// so the admin can fix the offending fields without re-typing.
func renderQuestionForm(
	w http.ResponseWriter,
	r *http.Request,
	renderer *TemplateRenderer,
	qctx *questionSaveCtx,
	fieldErrors map[string]string,
) {
	title := "Admin Dashboard - Question Edit"
	if qctx.IsNew {
		title = "Admin Dashboard - Question Create"
	}
	renderer.Render(w, r, http.StatusBadRequest, questionFormData{
		Title:       title,
		Quiz:        quizDataFromQuiz(qctx.Quiz),
		Question:    questionDataFromQuestion(qctx.Question),
		FieldErrors: fieldErrors,
	})
}
