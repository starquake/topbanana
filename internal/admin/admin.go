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

	"github.com/starquake/topbanana/internal/httputil"
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
	t      *template.Template
}

// NewTemplateRenderer creates a new TemplateRenderer with the given logger and template path.
// It parses the template on creation.
func NewTemplateRenderer(logger *slog.Logger, templatePath string) *TemplateRenderer {
	return &TemplateRenderer{
		logger: logger,
		t:      parseTemplate(templatePath),
	}
}

// Render renders a template to the writer (w) giving a templatePath and data.
// It does not return an error because the headers have already been written. So we can't render an error page anyway.
func (tr *TemplateRenderer) Render(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.WriteHeader(status)
	if err := tr.t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}

// QuizData is the data for the quiz list page, it shows multiple quizzes when available.
type QuizData struct {
	ID          int64
	Title       string
	Slug        string
	Description string
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

//nolint:gochecknoglobals // This is fine for now, will refactor to use a renderer later. TODO!
var layouts = template.Must(template.ParseFS(tmpl.FS, "admin/layouts/*.gohtml"))

const (
	maxOptions = 4
)

func quizDataFromQuiz(qz *quiz.Quiz) *QuizData {
	return &QuizData{
		ID:          qz.ID,
		Title:       qz.Title,
		Slug:        qz.Slug,
		Description: qz.Description,
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
func parseTemplate(path string) *template.Template {
	return template.Must(template.Must(layouts.Clone()).ParseFS(tmpl.FS, path))
}

// render400 renders the 400 error page with the given message.
// Should be used as the final handler in the chain and probably be followed by a return.
func render400(w http.ResponseWriter, r *http.Request, logger *slog.Logger, msg string) {
	render := NewTemplateRenderer(logger, "admin/errors/400.gohtml")
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
func render404(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	render := NewTemplateRenderer(logger, "admin/errors/404.gohtml")
	render.Render(w, r, http.StatusNotFound, nil)
}

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	render := NewTemplateRenderer(logger, "admin/errors/500.gohtml")
	render.Render(w, r, http.StatusInternalServerError, nil)
}

// quizByID returns the quiz with the given ID from the store. It includes the questions.
// It logs any errors that occur, renders the errorpage and returns false.
func quizByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	q, err := quizStore.GetQuiz(r.Context(), id)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) || errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.ErrorContext(r.Context(), "quiz not found", slog.Any("err", err))
			render404(w, r, logger)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching data", slog.Any("err", err))
		render500(w, r, logger)

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
			render404(w, r, logger)

			return nil, false
		}
		logger.ErrorContext(
			r.Context(),
			fmt.Sprintf("error fetching data for question with ID %d", questionID),
			slog.Any("err", err),
		)
		render500(w, r, logger)

		return nil, false
	}

	return qs, true
}

// fillQuizFromForm fills the quiz fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the quiz was filled successfully.
func fillQuizFromForm(w http.ResponseWriter, r *http.Request, logger *slog.Logger, qz *quiz.Quiz) bool {
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, msg)

		return false
	}
	qz.Title = r.PostFormValue("title")
	qz.Slug = r.PostFormValue("slug")
	qz.Description = r.PostFormValue("description")
	if problems := qz.Valid(r.Context()); len(problems) > 0 {
		msg := fmt.Sprintf("validation errors: %v", problems)
		logger.ErrorContext(r.Context(), msg)
		render400(w, r, logger, msg)

		return false
	}

	return true
}

// fillQuestionFromForm fills the question fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the question was filled successfully.
func fillQuestionFromForm(w http.ResponseWriter, r *http.Request, logger *slog.Logger, qs *quiz.Question) bool {
	var err error
	// Parse form values
	err = r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, msg)

		return false
	}

	qs.Text = r.PostFormValue("text")
	qs.ImageURL = r.PostFormValue("image_url")
	if qs.Position, err = strconv.Atoi(r.PostFormValue("position")); err != nil {
		msg := "error parsing position"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, msg)

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
			op.ID, err = httputil.IDFromString(r.PostFormValue(fmt.Sprintf("option[%d].id", i)))
			if err != nil {
				msg := "error parsing optionID"
				logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
				render400(w, r, logger, msg)

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
		render400(w, r, logger, msg)

		return false
	}

	return true
}

func storeQuiz(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	quizStore quiz.Store,
	qz *quiz.Quiz,
) bool {
	var err error
	if qz.ID == 0 {
		if err = quizStore.CreateQuiz(r.Context(), qz); err != nil {
			logger.ErrorContext(r.Context(), "error creating quiz", slog.Any("err", err))
			render500(w, r, logger)

			return false
		}
	} else {
		if err = quizStore.UpdateQuiz(r.Context(), qz); err != nil {
			logger.ErrorContext(r.Context(), "error updating quiz", slog.Any("err", err))
			render500(w, r, logger)

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
	quizStore quiz.Store,
	qs *quiz.Question,
) bool {
	var err error
	if qs.ID == 0 {
		err = quizStore.CreateQuestion(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error creating question", slog.Any("err", err))
			render500(w, r, logger)

			return false
		}
	} else {
		err = quizStore.UpdateQuestion(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error updating question", slog.Any("err", err))
			render500(w, r, logger)

			return false
		}
	}

	return true
}

// HandleIndex returns the index page.
func HandleIndex(logger *slog.Logger) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/index.gohtml")

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
func HandleQuizList(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizlist.gohtml")

	type quizListData struct {
		Title   string
		Quizzes []*QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving quizzes from store", slog.Any("err", err))
			render500(w, r, logger)

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
func HandleQuizView(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizview.gohtml")

	type quizViewData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var id int64
		if id, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, quizStore, id); !ok {
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
func HandleQuizCreate(logger *slog.Logger) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizform.gohtml")

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
func HandleQuizEdit(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizform.gohtml")

	type quizEditData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, quizStore, quizID); !ok {
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
func HandleQuizSave(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		newQuiz := quizID == 0
		var qz *quiz.Quiz
		if newQuiz {
			qz = &quiz.Quiz{}
		} else {
			if qz, ok = quizByID(w, r, logger, quizStore, quizID); !ok {
				return
			}
		}

		if !fillQuizFromForm(w, r, logger, qz) {
			return
		}

		if !storeQuiz(w, r, logger, quizStore, qz) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}

// HandleQuestionCreate creates a question.
func HandleQuestionCreate(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/questionform.gohtml")

	type questionCreateData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, quizStore, quizID); !ok {
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
func HandleQuestionEdit(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/questionform.gohtml")

	type questionEditData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var questionID int64
		if questionID, ok = httputil.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}
		newQuestion := questionID == 0

		qz, ok := quizByID(w, r, logger, quizStore, quizID)
		if !ok {
			return
		}

		var qs *quiz.Question

		if newQuestion {
			qs = &quiz.Question{
				QuizID: quizID,
			}
		} else {
			qs, ok = questionByID(w, r, logger, quizStore, questionID)
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

// HandleQuestionSave saves a question.
func HandleQuestionSave(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse quiz and question IDs from the URL
		var ok bool

		var quizID int64
		if quizID, ok = httputil.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var questionID int64
		if questionID, ok = httputil.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		newQuestion := questionID == 0

		// Retrieve quiz and question from the store
		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, quizStore, quizID); !ok {
			return
		}

		var qs *quiz.Question
		if newQuestion {
			qs = &quiz.Question{
				QuizID: qz.ID,
			}
		} else {
			if qs, ok = questionByID(w, r, logger, quizStore, questionID); !ok {
				return
			}
		}

		if !fillQuestionFromForm(w, r, logger, qs) {
			return
		}

		if !storeQuestion(w, r, logger, quizStore, qs) {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}
