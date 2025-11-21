// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
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

// QuizEditData is the data for the quiz edit page.
type QuizEditData struct {
	Title string
	Quiz  *QuizData
}

// QuizSaveData is the data for the quiz save page.
type QuizSaveData struct {
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
	Question *QuestionData
}

//nolint:gochecknoglobals // This is fine for now, will refactor to use a Renderer later. TODO!
var layouts = template.Must(template.ParseGlob("templates/admin/layouts/*.gohtml"))

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
		Position:   op.Position,
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

// HandleIndex returns the index page.
func HandleIndex(logger *logging.Logger) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/index.gohtml"),
	)

	data := IndexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// TODO: Move to middleware
			logger.Info(r.Context(), "Hello World", slog.String("handler", "HandleIndex"))
			err := tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				return
			}
		})
}

// HandleQuizList returns the quiz list page.
func HandleQuizList(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/quizlist.gohtml"),
	)

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var qzd []*QuizData
			var err error
			quizzes, err := quizStore.List(r.Context())
			if err != nil {
				logger.Error(r.Context(), "error getting quizzes", "err", err)

				return
			}

			qzd = quizDataFromQuizzes(quizzes)

			data := QuizListData{
				Title:   "Admin Dashboard - Quiz List",
				Quizzes: qzd,
			}

			err = tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				logger.Error(r.Context(), "error executing template", err)
			}
		})
}

// HandleQuizSave saves the quiz.
func HandleQuizSave(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/quizsave.gohtml"),
	)

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var err error
			err = r.ParseForm()
			if err != nil {
				logger.Error(r.Context(), "error parsing form", err)
			}

			quizID, err := strconv.ParseInt(r.PathValue("quizId"), 10, 64)
			if err != nil {
				logger.Error(r.Context(), "error parsing quizID", "err", err)

				return
			}

			qz, err := quizStore.GetByID(r.Context(), quizID)
			if err != nil {
				logger.Error(r.Context(), "error getting quiz", "err", err)

				return
			}

			qz.Title = r.FormValue("title")
			qz.Slug = r.FormValue("slug")
			qz.Description = r.FormValue("description")

			qzd := quizDataFromQuiz(qz)

			data := QuizSaveData{
				Title: "Admin Dashboard - Quiz Save",
				Quiz:  qzd,
			}

			err = tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				logger.Error(r.Context(), "error executing template", err)
			}
		})
}

// templateHandler returns a handler that renders a template with the given data.
func templateHandler(
	logger *logging.Logger,
	templateFile string,
	idPathName string,
	fetchAndBuild func(ctx context.Context, id int64) (any, error),
) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles(templateFile),
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue(idPathName), 10, 64)
		if err != nil {
			logger.Error(r.Context(), "error parsing ID", "err", err)

			return
		}

		data, err := fetchAndBuild(r.Context(), id)
		if err != nil {
			logger.Error(r.Context(), "error fetching data", "err", err)

			return
		}

		err = tmpl.ExecuteTemplate(w, "base.gohtml", data)
		if err != nil {
			logger.Error(r.Context(), "error executing template", "err", err)
		}
	})
}

// HandleQuizView returns the quiz view page.
func HandleQuizView(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return templateHandler(
		logger,
		"templates/admin/pages/quizview.gohtml",
		"quizId",
		func(ctx context.Context, id int64) (any, error) {
			q, err := quizStore.GetByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("error getting quiz: %w", err)
			}

			return QuizViewData{
				Title: "Admin Dashboard - Quiz List",
				Quiz:  quizDataFromQuiz(q),
			}, nil
		},
	)
}

// HandleQuizEdit returns the quiz edit page.
func HandleQuizEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return templateHandler(
		logger,
		"templates/admin/pages/quizedit.gohtml",
		"quizId",
		func(ctx context.Context, id int64) (any, error) {
			qz, err := quizStore.GetByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("error getting quiz: %w", err)
			}

			return QuizEditData{
				Title: "Admin Dashboard - Quiz Edit",
				Quiz:  quizDataFromQuiz(qz),
			}, nil
		},
	)
}

// HandleQuestionEdit returns the question edit page.
func HandleQuestionEdit(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	return templateHandler(
		logger,
		"templates/admin/pages/questionedit.gohtml",
		"questionId",
		func(ctx context.Context, id int64) (any, error) {
			qs, err := quizStore.GetQuestionByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("error getting question: %w", err)
			}

			return QuestionEditData{
				Title:    "Admin Dashboard - Quiz Edit",
				Question: questionDataFromQuestion(qs),
			}, nil
		},
	)
}
