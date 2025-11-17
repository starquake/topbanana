package admin

import (
	"html/template"
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
)

type IndexData struct {
	Title string
}

type QuizListData struct {
	Title   string
	Quizzes []quiz.Quiz
}

var layouts = template.Must(template.ParseGlob("templates/admin/layouts/*.gohtml"))

func HandleAdminIndex(logger *logging.Logger) http.Handler {

	tmpl := template.Must(template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/index.gohtml"))

	data := IndexData{
		Title: "Admin Dashboard",
	}

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// TODO: Move to middleware
			logger.Info(r.Context(), "Hello World", "handler", "handleAdminIndex")
			err := tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				return
			}
		})
}

func HandleAdminQuizList(logger *logging.Logger) http.Handler {
	tmpl := template.Must(template.Must(layouts.Clone()).ParseFiles("templates/admin/pages/quizlist.gohtml"))

	data := QuizListData{
		Title: "Admin Dashboard - Quiz List",
		Quizzes: []quiz.Quiz{
			{
				ID:          1,
				Name:        "Quiz 1",
				Slug:        "quiz-1",
				Description: "Quiz 1 Description",
			},
		},
	}

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			logger.Info(r.Context(), "Hello World", "handler", "handleAdminQuizList")
			err := tmpl.ExecuteTemplate(w, "base.gohtml", data)
			if err != nil {
				return
			}
		})
}
