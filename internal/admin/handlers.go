// Package admin contains handlers for the admin dashboard
package admin

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
)

// IndexData is the data for the index page.
type IndexData struct {
	Title string
}

// QuizData is the data for the quiz list page.
type QuizData struct {
	ID          int64
	Title       string
	Slug        string
	Description string
}

// QuizListData is the data for list on the quiz list page.
type QuizListData struct {
	Title   string
	Quizzes []QuizData
}

//nolint:gochecknoglobals // This is fine for now, will refactor to use a Renderer later. TODO!
var layouts = template.Must(template.ParseGlob("templates/admin/layouts/*.gohtml"))

// HandleAdminIndex returns the index page.
func HandleAdminIndex(logger *logging.Logger) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/index.gohtml"),
	)

	data := IndexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// TODO: Move to middleware
			logger.Info(r.Context(), "Hello World", slog.String("handler", "HandleAdminIndex"))
			err := tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				return
			}
		})
}

// HandleAdminQuizList returns the quiz list page.
func HandleAdminQuizList(logger *logging.Logger, quizStore quiz.Store) http.Handler {
	tmpl := template.Must(
		template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/quizlist.gohtml"),
	)

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var qd []QuizData
			var err error
			quizzes, err := quizStore.List(r.Context())
			if err != nil {
				logger.Error(r.Context(), "error getting quizzes", "err", err)

				return
			}
			qd = make([]QuizData, 0, len(quizzes))
			for _, q := range quizzes {
				qd = append(qd, QuizData{
					ID:          q.ID,
					Title:       q.Title,
					Slug:        q.Slug,
					Description: q.Description,
				})
			}

			data := QuizListData{
				Title:   "Admin Dashboard - Quiz List",
				Quizzes: qd,
			}

			err = tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				logger.Error(r.Context(), "error executing template", err)
			}
		})
}
