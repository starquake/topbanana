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

	error404Template = "admin/errors/404.gohtml"
	error500Template = "admin/errors/500.gohtml"
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

// parseIDFromPath parses an int64 ID from the given path value.
func parseIDFromPath(r *http.Request, logger *logging.Logger, pathKey string) (int64, error) {
	pathValue := r.PathValue(pathKey)
	if pathValue == "" {
		logger.Info(r.Context(), "path value is empty", logging.String("pathKey", pathKey))

		return 0, nil
	}
	id, err := strconv.ParseInt(pathValue, base10, int64Size)
	if err != nil {
		logger.Error(r.Context(), "error parsing ID", "err", err, "pathKey", pathKey)

		return 0, fmt.Errorf("error parsing ID: %w", err)
	}

	return id, nil
}

// executeTemplate executes a template and logs any errors.
// It does not return an error because the headers have already been written. So we can't render an error page anyway.
func executeTemplate(w http.ResponseWriter, r *http.Request, logger *logging.Logger, t *template.Template, data any) {
	err := t.ExecuteTemplate(w, "base.gohtml", data)
	if err != nil {
		logger.Error(r.Context(), "error executing template", "err", err)
	}
}

// render404 renders the 404 error page.
func render404(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	t := parseTemplate(error404Template)
	executeTemplate(w, r, logger, t, nil)
}

// render500 renders the 500 error page.
func render500(w http.ResponseWriter, r *http.Request, logger *logging.Logger) {
	t := parseTemplate(error500Template)
	executeTemplate(w, r, logger, t, nil)
}

// HandleIndex returns the index page.
func HandleIndex(logger *logging.Logger) http.Handler {
	t := parseTemplate("admin/pages/index.gohtml")

	data := IndexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuizList returns the quiz list page.
func HandleQuizList(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizlist.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.Error(r.Context(), "error getting quizzes", "err", err)
			render500(w, r, logger)

			return
		}

		qzd := quizDataFromQuizzes(quizzes)

		data := QuizListData{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
		}

		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuizView returns the quiz view page.
func HandleQuizView(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		id, err := parseIDFromPath(r, logger, "quizId")
		if err != nil {
			logger.Error(r.Context(), "error parsing ID", "err", err)
			render500(w, r, logger)

			return
		}

		q, err := quizStore.GetQuizByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", "err", err)
				render404(w, r, logger)

				return
			}
			logger.Error(r.Context(), "error fetching data", "err", err)
			render500(w, r, logger)

			return
		}
		data := QuizViewData{
			Title: "Admin Dashboard - Quiz View",
			Quiz:  quizDataFromQuiz(q),
		}
		executeTemplate(w, r, logger, t, data)
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
		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuizEdit handles the display of the quiz edit page in the admin dashboard.
func HandleQuizEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		id, err := parseIDFromPath(r, logger, "quizId")
		if err != nil {
			return
		}

		q, err := quizStore.GetQuizByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", "err", err)
				render404(w, r, logger)

				return
			}
			logger.Error(r.Context(), "error fetching data", "err", err)
			render500(w, r, logger)

			return
		}
		data := QuizEditData{
			Title: "Admin Dashboard - Quiz Edit",
			Quiz:  quizDataFromQuiz(q),
		}
		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuizSave saves the quiz to the database.
func HandleQuizSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		err = r.ParseForm()
		if err != nil {
			logger.Error(r.Context(), "error parsing form", err)
		}

		var quizID int64
		quizID, err = parseIDFromPath(r, logger, "quizId")
		if err != nil {
			logger.Error(r.Context(), "error parsing quiz ID", "err", err)

			return
		}
		newQuiz := quizID == 0

		var qz *quiz.Quiz
		if newQuiz {
			qz = &quiz.Quiz{}
		} else {
			qz, err = quizStore.GetQuizByID(r.Context(), quizID)
			if err != nil {
				logger.Error(r.Context(), "error getting quiz", "err", err)

				return
			}
		}

		qz.Title = r.PostFormValue("title")
		qz.Slug = r.PostFormValue("slug")
		qz.Description = r.PostFormValue("description")

		if newQuiz {
			err = quizStore.CreateQuiz(r.Context(), qz)
			if err != nil {
				logger.Error(r.Context(), "error creating quiz", "err", err)

				return
			}
		} else {
			err = quizStore.UpdateQuiz(r.Context(), qz)
			if err != nil {
				logger.Error(r.Context(), "error updating quiz", "err", err)

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

		quizID, err := parseIDFromPath(r, logger, "quizId")
		if err != nil {
			logger.Error(r.Context(), "error parsing quiz ID", "err", err)

			return
		}

		var qz *quiz.Quiz
		qz, err = quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", "err", err)
				render404(w, r, logger)
			}
		}

		data := QuestionEditData{
			Title:    "Admin Dashboard - Question Create",
			Quiz:     quizDataFromQuiz(qz),
			Question: &QuestionData{},
		}
		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuestionEdit handles the display of the question edit page in the admin dashboard.
func HandleQuestionEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	t := parseTemplate("admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		var quizID int64
		quizID, err = parseIDFromPath(r, logger, "quizId")
		if err != nil {
			logger.Error(r.Context(), "error parsing quiz ID", "err", err)

			return
		}

		var qz *quiz.Quiz
		qz, err = quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				logger.Error(r.Context(), "quiz not found", "err", err)
				render404(w, r, logger)
			}
		}

		var questionID int64
		questionID, err = parseIDFromPath(r, logger, "questionId")
		if err != nil {
			logger.Error(r.Context(), "error parsing qs ID", "err", err)

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
					logger.Error(r.Context(), "qs not found", "err", err)
					render404(w, r, logger)

					return
				}
				logger.Error(r.Context(), "error fetching data", "err", err)
				render500(w, r, logger)

				return
			}
		}

		data := QuestionEditData{
			Title:    "Admin Dashboard - Question Edit",
			Quiz:     quizDataFromQuiz(qz),
			Question: questionDataFromQuestion(qs),
		}
		executeTemplate(w, r, logger, t, data)
	})
}

// HandleQuestionSave saves a question.
func HandleQuestionSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		var quizID int64
		quizID, err = parseIDFromPath(r, logger, "quizId")
		if err != nil {
			logger.Error(r.Context(), "error parsing quiz ID", "err", err)

			return
		}

		var questionID int64
		questionID, err = parseIDFromPath(r, logger, "questionId")
		if err != nil {
			logger.Error(r.Context(), "error parsing question ID", "err", err)

			return
		}
		newQuestion := questionID == 0

		err = r.ParseForm()
		if err != nil {
			logger.Error(r.Context(), "error parsing form", err)
		}

		qz, err := quizStore.GetQuizByID(r.Context(), quizID)
		if err != nil {
			logger.Error(r.Context(), "error getting quiz", "err", err)

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
				logger.Error(r.Context(), "error getting question", "err", err)

				return
			}
		}

		qs.Text = r.PostFormValue("text")
		qs.ImageURL = r.PostFormValue("imageUrl")
		position, err := strconv.Atoi(r.PostFormValue("position"))
		if err != nil {
			logger.Error(r.Context(), "error parsing position", "err", err)

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
			opID := r.PostFormValue(fmt.Sprintf("option[%d]id", i))
			if opID == "" {
				op.ID = 0
			} else {
				op.ID, err = strconv.ParseInt(opID, base10, int64Size)
				if err != nil {
					logger.Error(r.Context(), "error parsing option ID", "err", err)

					return
				}
			}
			op.Text = r.PostFormValue(fmt.Sprintf("option[%d]text", i))
			op.Correct = r.PostFormValue(fmt.Sprintf("option[%d]correct", i)) == "on"

			newOptions = append(newOptions, op)
		}
		qs.Options = newOptions

		if newQuestion {
			err = quizStore.CreateQuestion(r.Context(), qs)
			if err != nil {
				logger.Error(r.Context(), "error creating question", "err", err)

				return
			}
		} else {
			err = quizStore.UpdateQuestion(r.Context(), qs)
			if err != nil {
				logger.Error(r.Context(), "error updating question", "err", err)

				return
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusFound)
	})
}
