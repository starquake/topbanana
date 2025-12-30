package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/store"
)

func addRoutes(mux *http.ServeMux, logger *slog.Logger, stores *store.Stores) {
	mux.Handle("GET /admin", admin.HandleIndex(logger))
	mux.Handle("GET /admin/quizzes", admin.HandleQuizList(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/{quizID}", admin.HandleQuizView(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/new", admin.HandleQuizCreate(logger))
	mux.Handle("POST /admin/quizzes", admin.HandleQuizSave(logger, stores.Quizzes))
	mux.Handle("GET /admin/quizzes/{quizID}/edit", admin.HandleQuizEdit(logger, stores.Quizzes))
	mux.Handle("POST /admin/quizzes/{quizID}", admin.HandleQuizSave(logger, stores.Quizzes))

	mux.Handle("GET /admin/quizzes/{quizID}/questions/new", admin.HandleQuestionCreate(logger, stores.Quizzes))
	mux.Handle("POST /admin/quizzes/{quizID}/questions", admin.HandleQuestionSave(logger, stores.Quizzes))
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/{questionID}/edit",
		admin.HandleQuestionEdit(logger, stores.Quizzes),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}",
		admin.HandleQuestionSave(logger, stores.Quizzes),
	)

	mux.Handle("GET /healthz", health.HandleHealthz(logger, stores))

	mux.Handle("/", http.NotFoundHandler())
}
