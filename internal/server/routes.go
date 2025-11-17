package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/logging"
)

func addRoutes(
	mux *http.ServeMux,
	logger *logging.Logger,
) {
	mux.Handle("/admin", admin.HandleAdminIndex(logger))
	mux.Handle("/admin/quizzes", admin.HandleAdminQuizList(logger))
	mux.Handle("/", http.NotFoundHandler())
}
