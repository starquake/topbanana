// Package admin contains handlers for the admin dashboard
package admin

import (
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

// IndexData is the data for the index page.
type IndexData struct {
	Title string
}

// QuizListData is the data for the list on the quiz list page.
type QuizListData struct {
	Title   string
	Quizzes []*QuizData
}

// QuizViewData is the data for the quiz view page.
type QuizViewData struct {
	Title string
	Quiz  *QuizData
}

// QuizCreateData is the data for the quiz create page.
type QuizCreateData struct {
	Title string
}

// QuizEditData is the data for the quiz edit page.
type QuizEditData struct {
	Title string
	Quiz  *QuizData
}

// QuizData is the data for the quiz list page.
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

// QuestionEditData is the data for the question edit page.
type QuestionEditData struct {
	Title    string
	Quiz     *QuizData
	Question *QuestionData
}

//nolint:gochecknoglobals // This is fine for now, will refactor to use a Renderer later. TODO!
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
		return 0, fmt.Errorf("error parsing ID: %w", err)
	}

	return id, nil
}

// executeTemplate executes a template and logs any errors.
// It does not return an error because the headers have already been written. So we can't render an error page anyway.
func executeTemplate(w http.ResponseWriter, t *template.Template, data any) error {
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		return fmt.Errorf("error executing template: %w", err)
	}

	return nil
}

func render400(w http.ResponseWriter, r *http.Request, logger *logging.Logger, msg string) {
	t := parseTemplate("admin/errors/400.gohtml")
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Error",
		Message: msg,
	}
	if err := executeTemplate(w, t, data); err != nil {
		logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
	}
}

// render404 renders the 404 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render404(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	t := parseTemplate("admin/errors/404.gohtml")
	if err := executeTemplate(w, t, nil); err != nil {
		logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
	}
}

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	t := parseTemplate("admin/errors/500.gohtml")
	if err := executeTemplate(w, t, nil); err != nil {
		logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
	}
}

// HandleIndex returns the index page.
func HandleIndex(logger *logging.Logger) http.Handler {
	t := parseTemplate("admin/pages/index.gohtml")

	data := IndexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuizList returns the quiz list page.
func HandleQuizList(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizlist.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.Error(r.Context(), "error retrieving quizzes from store", logging.ErrAttr(err))
			render500(w, r, logger)

			return
		}

		qzd := quizDataFromQuizzes(quizzes)

		data := QuizListData{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
		}

		if err = executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuizView returns the quiz view page.
func HandleQuizView(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		id, err := idFromString(r.PathValue("quizId"))
		if err != nil {
			msg := "error parsing quiz ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}

		q, err := quizStore.GetQuizByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", logging.ErrAttr(err))
				render404(w, r, logger)

				return
			}
			logger.Error(r.Context(), "error fetching data", logging.ErrAttr(err))
			render500(w, r, logger)

			return
		}
		data := QuizViewData{
			Title: "Admin Dashboard - Quiz View",
			Quiz:  quizDataFromQuiz(q),
		}
		if err = executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuizCreate creates a quiz.
func HandleQuizCreate(logger *logging.Logger) http.Handler {
	t := parseTemplate("admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := QuizEditData{
			Title: "Admin Dashboard - Quiz Create",
			Quiz:  &QuizData{},
		}
		if err := executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuizEdit handles the display of the quiz edit page in the admin dashboard.
func HandleQuizEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		id, err := idFromString(r.PathValue("quizId"))
		if err != nil {
			return
		}

		q, err := quizStore.GetQuizByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", logging.ErrAttr(err))
				render404(w, r, logger)

				return
			}
			logger.Error(r.Context(), "error fetching data", logging.ErrAttr(err))
			render500(w, r, logger)

			return
		}
		data := QuizEditData{
			Title: "Admin Dashboard - Quiz Edit",
			Quiz:  quizDataFromQuiz(q),
		}
		if err = executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuizSave saves the quiz to the database.
func HandleQuizSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		if err = r.ParseForm(); err != nil {
			logger.Error(r.Context(), "error parsing form", logging.ErrAttr(err))
		}

		var quizID int64
		quizID, err = idFromString(r.PathValue("quizId"))
		if err != nil {
			msg := "error parsing quiz ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}
		newQuiz := quizID == 0

		var qz *quiz.Quiz
		if newQuiz {
			qz = &quiz.Quiz{}
		} else {
			qz, err = quizStore.GetQuizByID(r.Context(), quizID)
			if err != nil {
				logger.Error(r.Context(), "error retrieving quiz from store", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		}

		qz.Title = r.PostFormValue("title")
		qz.Slug = r.PostFormValue("slug")
		qz.Description = r.PostFormValue("description")

		if newQuiz {
			if err = quizStore.CreateQuiz(r.Context(), qz); err != nil {
				logger.Error(r.Context(), "error creating quiz", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		} else {
			if err = quizStore.UpdateQuiz(r.Context(), qz); err != nil {
				logger.Error(r.Context(), "error updating quiz", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusFound)
	})
}

// HandleQuestionCreate creates a question.
func HandleQuestionCreate(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizID, err := idFromString(r.PathValue("quizId"))
		if err != nil {
			msg := "error parsing quiz ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}

		var qz *quiz.Quiz
		qz, err = quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", logging.ErrAttr(err))
				render404(w, r, logger)

				return
			}
		}

		data := QuestionEditData{
			Title:    "Admin Dashboard - Question Create",
			Quiz:     quizDataFromQuiz(qz),
			Question: &QuestionData{},
		}
		if err = executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuestionEdit handles the display of the question edit page in the admin dashboard.
func HandleQuestionEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, err := idFromString(r.PathValue("quizId"))
		if err != nil {
			msg := "error parsing quiz ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}

		qz, err := quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", logging.ErrAttr(err))
				render404(w, r, logger)

				return
			}
		}

		questionID, err := idFromString(r.PathValue("questionId"))
		if err != nil {
			msg := "error parsing question ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}
		newQuestion := questionID == 0

		var qs *quiz.Question

		if newQuestion {
			qs = &quiz.Question{
				QuizID: quizID,
			}
		} else {
			qs, err = quizStore.GetQuestionByID(r.Context(), questionID)
			if err != nil {
				if errors.Is(err, quiz.ErrQuestionNotFound) {
					logger.Error(r.Context(), "question not found", logging.ErrAttr(err))
					render404(w, r, logger)

					return
				}
				logger.Error(r.Context(), "error fetching data", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		}

		data := QuestionEditData{
			Title:    "Admin Dashboard - Question Edit",
			Quiz:     quizDataFromQuiz(qz),
			Question: questionDataFromQuestion(qs),
		}
		if err = executeTemplate(w, t, data); err != nil {
			logger.Error(r.Context(), "error executing template", logging.ErrAttr(err))
		}
	})
}

// HandleQuestionSave saves a question.
func HandleQuestionSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		var quizID int64
		quizID, err = idFromString(r.PathValue("quizId"))
		if err != nil {
			msg := "error parsing quiz ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}

		var questionID int64
		questionID, err = idFromString(r.PathValue("questionId"))
		if err != nil {
			msg := "error parsing question ID"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}
		newQuestion := questionID == 0

		err = r.ParseForm()
		if err != nil {
			logger.Error(r.Context(), "error parsing form", logging.ErrAttr(err))
		}

		qz, err := quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			logger.Error(r.Context(), "error retrieving quiz from store", logging.ErrAttr(err))

			return
		}

		var qs *quiz.Question

		if newQuestion {
			qs = &quiz.Question{
				QuizID: qz.ID,
			}
		} else {
			qs, err = quizStore.GetQuestionByID(r.Context(), questionID)
			if err != nil {
				logger.Error(r.Context(), "error retrieving question from store", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		}

		qs.Text = r.PostFormValue("text")
		qs.ImageURL = r.PostFormValue("imageUrl")
		position, err := strconv.Atoi(r.PostFormValue("position"))
		if err != nil {
			msg := "error parsing position"
			logger.Error(r.Context(), msg, logging.ErrAttr(err))
			render400(w, r, logger, msg)

			return
		}
		qs.Position = position

		newOptions := make([]*quiz.Option, 0, maxOptions)

		for _, i := range []int{0, 1, 2, 3} {
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

				return
			}
			op.Text = r.PostFormValue(fmt.Sprintf("option[%d]text", i))
			op.Correct = r.PostFormValue(fmt.Sprintf("option[%d]correct", i)) == "on"

			newOptions = append(newOptions, op)
		}
		qs.Options = newOptions

		if newQuestion {
			err = quizStore.CreateQuestion(r.Context(), qs)
			if err != nil {
				logger.Error(r.Context(), "error creating question", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		} else {
			err = quizStore.UpdateQuestion(r.Context(), qs)
			if err != nil {
				logger.Error(r.Context(), "error updating question", logging.ErrAttr(err))
				render500(w, r, logger)

				return
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusFound)
	})
}
