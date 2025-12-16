// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// Validator is an interface for validating data.
type Validator interface {
	Valid(ctx context.Context) map[string]string
}

// TemplateRenderer renders templates using the given logger and template path.
type TemplateRenderer struct {
	logger *logging.Logger
	t      *template.Template
}

// NewTemplateRenderer creates a new TemplateRenderer with the given logger and template path.
// It parses the template on creation.
func NewTemplateRenderer(logger *logging.Logger, templatePath string) *TemplateRenderer {
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
		tr.logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
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
	base10    = 10
	int64Size = 64

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
		func(a *QuestionData, b *QuestionData) int { return a.Position - b.Position },
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
		func(a *OptionData, b *OptionData) int { return a.Position - b.Position },
	)

	return data
}

// parseTemplate parses a template from the given path with layouts.
func parseTemplate(path string) *template.Template {
	return template.Must(template.Must(layouts.Clone()).ParseFS(tmpl.FS, path))
}

// idFromString parses an int64 ID from the given string.
// returns 0 if the path value is empty.
func idFromString(pathValue string) (int64, error) {
	if pathValue == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(pathValue, base10, int64Size)
	if err != nil {
		return 0, fmt.Errorf("error parsing %q: %w", pathValue, err)
	}

	return id, nil
}

// parseIDFromPath parses an int64 ID from the given path value.
// It returns the parsed ID and true if the parsing was successful.
// It returns 0 and true if the path value is empty.
// It renders a 400 error page if the path value cannot be parsed.
func parseIDFromPath(w http.ResponseWriter, r *http.Request, logger *logging.Logger, s string) (int64, bool) {
	pathValue := r.PathValue(s)
	if pathValue == "" {
		return 0, true
	}

	id, err := idFromString(pathValue)
	if err != nil {
		msg := "error parsing " + s
		logger.Error(r.Context(), msg, logging.ErrAttr(err))
		render400(w, r, logger, msg)

		return 0, false
	}

	return id, true
}

// render400 renders the 400 error page with the given message.
// Should be used as the final handler in the chain and probably be followed by a return.
func render400(w http.ResponseWriter, r *http.Request, logger *logging.Logger, msg string) {
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
func render404(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	render := NewTemplateRenderer(logger, "admin/errors/404.gohtml")
	render.Render(w, r, http.StatusNotFound, nil)
}

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	render := NewTemplateRenderer(logger, "admin/errors/500.gohtml")
	render.Render(w, r, http.StatusInternalServerError, nil)
}

// quizByID returns the quiz with the given ID from the store.
// It logs any errors that occur, renders the errorpage and returns false.
func quizByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *logging.Logger,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	q, err := quizStore.GetQuizByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			logger.Error(r.Context(), "quiz not found", logging.ErrAttr(err))
			render404(w, r, logger)

			return nil, false
		}
		logger.Error(r.Context(), "error fetching data", logging.ErrAttr(err))
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
	logger *logging.Logger,
	quizStore quiz.Store,
	questionID int64,
) (*quiz.Question, bool) {
	qs, err := quizStore.GetQuestionByID(r.Context(), questionID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.Error(r.Context(), fmt.Sprintf("question with ID %d not found", questionID), logging.ErrAttr(err))

			return nil, false
		}
		logger.Error(
			r.Context(),
			fmt.Sprintf("error fetching data for question with ID %d", questionID),
			logging.ErrAttr(err),
		)
		render500(w, r, logger)

		return nil, false
	}

	return qs, true
}

// fillQuizFromForm fills the quiz fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the quiz was filled successfully.
func fillQuizFromForm(w http.ResponseWriter, r *http.Request, logger *logging.Logger, qz *quiz.Quiz) bool {
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.Error(r.Context(), msg, logging.ErrAttr(err))
		render400(w, r, logger, msg)

		return false
	}
	qz.Title = r.PostFormValue("title")
	qz.Slug = r.PostFormValue("slug")
	qz.Description = r.PostFormValue("description")
	if problems := qz.Valid(r.Context()); len(problems) > 0 {
		render400(w, r, logger, fmt.Sprintf("validation errors: %v", problems))

		return false
	}

	return true
}

// fillQuestionFromForm fills the question fields from the form values.
// It renders an error page if the form is invalid.
// It returns true if the form was valid and the question was filled successfully.
func fillQuestionFromForm(w http.ResponseWriter, r *http.Request, logger *logging.Logger, qs *quiz.Question) bool {
	var err error
	// Parse form values
	err = r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.Error(r.Context(), msg, logging.ErrAttr(err))
		render400(w, r, logger, msg)

		return false
	}

	qs.Text = r.PostFormValue("text")
	qs.ImageURL = r.PostFormValue("imageUrl")
	position, err := strconv.Atoi(r.PostFormValue("position"))
	if err != nil {
		msg := "error parsing position"
		logger.Error(r.Context(), msg, logging.ErrAttr(err))
		render400(w, r, logger, msg)

		return false
	}
	qs.Position = position

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
		op.ID, err = idFromString(r.PostFormValue(fmt.Sprintf("option[%d]id", i)))
		if err != nil {
			logger.Error(r.Context(), "error parsing option ID", logging.ErrAttr(err))
			render500(w, r, logger)

			return false
		}
		op.Text = r.PostFormValue(fmt.Sprintf("option[%d]text", i))
		op.Correct = r.PostFormValue(fmt.Sprintf("option[%d]correct", i)) == "on"

		newOptions = append(newOptions, op)
	}
	qs.Options = newOptions

	return true
}

func storeQuiz(
	w http.ResponseWriter,
	r *http.Request,
	logger *logging.Logger,
	quizStore quiz.Store,
	qz *quiz.Quiz,
) bool {
	var err error
	if qz.ID == 0 {
		if err = quizStore.CreateQuiz(r.Context(), qz); err != nil {
			logger.Error(r.Context(), "error creating quiz", logging.ErrAttr(err))
			render500(w, r, logger)

			return false
		}
	} else {
		if err = quizStore.UpdateQuiz(r.Context(), qz); err != nil {
			logger.Error(r.Context(), "error updating quiz", logging.ErrAttr(err))
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
	logger *logging.Logger,
	quizStore quiz.Store,
	qs *quiz.Question,
) bool {
	var err error
	if qs.ID == 0 {
		err = quizStore.CreateQuestion(r.Context(), qs)
		if err != nil {
			logger.Error(r.Context(), "error creating question", logging.ErrAttr(err))
			render500(w, r, logger)

			return false
		}
	} else {
		err = quizStore.UpdateQuestion(r.Context(), qs)
		if err != nil {
			logger.Error(r.Context(), "error updating question", logging.ErrAttr(err))
			render500(w, r, logger)

			return false
		}
	}

	return true
}

// HandleIndex returns the index page.
func HandleIndex(logger *logging.Logger) http.Handler {
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
func HandleQuizList(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizlist.gohtml")

	type quizListDAta struct {
		Title   string
		Quizzes []*QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.Error(r.Context(), "error retrieving quizzes from store", logging.ErrAttr(err))
			render500(w, r, logger)

			return
		}

		qzd := quizDataFromQuizzes(quizzes)

		data := quizListDAta{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
		}

		render.Render(w, r, http.StatusOK, data)
	})
}

// HandleQuizView returns the quiz view page.
func HandleQuizView(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizview.gohtml")

	type quizViewData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := quizByID(w, r, logger, quizStore, id)
		if !ok {
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
func HandleQuizCreate(logger *logging.Logger) http.Handler {
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
func HandleQuizEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/quizform.gohtml")

	type quizEditData struct {
		Title string
		Quiz  *QuizData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := parseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := quizByID(w, r, logger, quizStore, quizID)
		if !ok {
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
func HandleQuizSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var quizID int64
		var ok bool
		if quizID, ok = parseIDFromPath(w, r, logger, "quizID"); !ok {
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

		if ok = fillQuizFromForm(w, r, logger, qz); !ok {
			return
		}

		if ok = storeQuiz(w, r, logger, quizStore, qz); !ok {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusFound)
	})
}

// HandleQuestionCreate creates a question.
func HandleQuestionCreate(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/questionform.gohtml")

	type questionCreateData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, _ := parseIDFromPath(w, r, logger, "quizID")
		qz, ok := quizByID(w, r, logger, quizStore, quizID)
		if !ok {
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
func HandleQuestionEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, "admin/pages/questionform.gohtml")

	type questionEditData struct {
		Title    string
		Quiz     *QuizData
		Question *QuestionData
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, _ := parseIDFromPath(w, r, logger, "quizID")
		questionID, _ := parseIDFromPath(w, r, logger, "questionID")
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
func HandleQuestionSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse quiz and question IDs from the URL
		quizID, _ := parseIDFromPath(w, r, logger, "quizID")
		questionID, _ := parseIDFromPath(w, r, logger, "questionID")
		newQuestion := questionID == 0

		// Retrieve quiz and question from the store
		var qz *quiz.Quiz
		var ok bool
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

		if ok = fillQuestionFromForm(w, r, logger, qs); !ok {
			return
		}

		if problems := qs.Valid(r.Context()); len(problems) > 0 {
			render400(w, r, logger, fmt.Sprintf("validation errors: %v", problems))

			return
		}

		if ok = storeQuestion(w, r, logger, quizStore, qs); !ok {
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", quizID), http.StatusFound)
	})
}
