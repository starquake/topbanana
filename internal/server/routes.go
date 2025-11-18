package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/store"
)

func addRoutes(mux *http.ServeMux, logger *logging.Logger, stores *store.Stores) {
	mux.Handle("/admin", admin.HandleAdminIndex(logger))
	mux.Handle("/admin/quizzes", admin.HandleAdminQuizList(logger, stores.Quizzes))
	mux.Handle("/", http.NotFoundHandler())
}
