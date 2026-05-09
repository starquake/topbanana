// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gosimple/slug"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
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

// Render renders a template to the writer (w) giving a templatePath and data.
// It does not return an error because the headers have already been written. So we can't render an error page anyway.
//
// The clone-and-override dance lets the navbar template call {{currentUser}}
// without every handler having to thread the username into its data struct:
// the placeholder registered in parseTemplate satisfies the parser, and here
// we swap in a real implementation that reads the authenticated player from
// the request context.
//
// The same dance also wires up {{csrfToken}}: parseTemplate registers a
// placeholder so templates parse cleanly; here we replace it with one that
// asks the CSRF manager for the token, which sets a nonce cookie on the
// response if needed. Calling Token before WriteHeader is required because
// [http.SetCookie] is a header write.
func (tr *TemplateRenderer) Render(w http.ResponseWriter, r *http.Request, status int, data any) {
	t, err := tr.t.Clone()
	if err != nil {
		tr.logger.ErrorContext(r.Context(), "error cloning template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	username := ""
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		username = p.Username
	}

	csrfToken := ""
	if tr.csrf != nil {
		csrfToken = tr.csrf.Token(w, r)
	}

	t = t.Funcs(template.FuncMap{
		"currentUser": func() string { return username },
		"csrfToken":   func() string { return csrfToken },
	})

	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}

// QuizData is the data for the quiz list page, it shows multiple quizzes when available.
type QuizData struct {
	ID          int64
	Title       string
	Slug        string
	Description string
	UpdatedAt   time.Time
	Questions   []*QuestionData
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

func quizDataFromQuiz(qz *quiz.Quiz) *QuizData {
	return &QuizData{
		ID:          qz.ID,
		Title:       qz.Title,
		Slug:        qz.Slug,
		Description: qz.Description,
		UpdatedAt:   qz.UpdatedAt,
		Questions:   questionDataFromQuestions(qz.Questions),
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
	layouts := template.Must(
		template.New("").Funcs(funcs).ParseFS(tmpl.FS, "admin/layouts/*.gohtml"),
	)

	return template.Must(template.Must(layouts.Clone()).ParseFS(tmpl.FS, path))
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

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager) {
	render := &TemplateRenderer{logger: logger, csrf: csrfMgr, t: parseTemplate("admin/errors/500.gohtml")}
	render.Render(w, r, http.StatusInternalServerError, nil)
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
	if qs.Position, err = strconv.Atoi(r.PostFormValue("position")); err != nil {
		msg := "error parsing position"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return false
	}

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

		qzd := quizDataFromQuizzes(quizzes)

		data := quizListData{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
		}

		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuizView returns the quiz view page.
func HandleQuizView(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	type quizViewData struct {
		Title string
		Quiz  *QuizData
	}

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

		data := quizViewData{
			Title: "Admin Dashboard - View Quiz",
			Quiz:  quizDataFromQuiz(qz),
		}
		render.Render(w, r, http.StatusOK, data)
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

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		data := quizEditData{
			Title: "Admin Dashboard - Edit Quiz",
			Quiz:  quizDataFromQuiz(qz),
		}
		render.Render(w, r, http.StatusOK, data)
	})
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
			qz = &quiz.Quiz{}
		} else {
			if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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

		qz, ok := quizByID(w, r, logger, csrfMgr, quizStore, quizID)
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

// HandleQuestionDelete deletes a question and all its options.
func HandleQuestionDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
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

		// Retrieve quiz and question from the store
		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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

		if !storeQuestion(w, r, logger, csrfMgr, quizStore, qs) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}
