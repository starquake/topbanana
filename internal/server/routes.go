package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/store"
)

func addRoutes(mux *http.ServeMux, logger *logging.Logger, stores *store.Stores) {
	mux.Handle("GET /admin", admin.HandleIndex(logger))
	mux.Handle("GET /admin/quizzes", admin.HandleQuizList(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/{quizId}", admin.HandleQuizView(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/new", admin.HandleQuizCreate(logger))
	mux.Handle("POST /admin/quizzes/save", admin.HandleQuizSave(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/{quizId}/edit", admin.HandleQuizEdit(logger, stores.Quizzes))
	mux.Handle("POST /admin/quizzes/{quizId}/save", admin.HandleQuizSave(logger, stores.Quizzes))

	mux.Handle("GET /admin/quizzes/{quizId}/questions/new", admin.HandleQuestionCreate(logger, stores.Quizzes))
	mux.Handle("POST /admin/quizzes/{quizId}/questions/save", admin.HandleQuestionSave(logger, stores.Quizzes))
	mux.Handle(
		"GET /admin/quizzes/{quizId}/questions/{questionId}/edit",
		admin.HandleQuestionEdit(logger, stores.Quizzes),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizId}/questions/{questionId}/save",
		admin.HandleQuestionSave(logger, stores.Quizzes),
	)

	mux.Handle("/", http.NotFoundHandler())
}
