package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/client"
	"github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func addRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	cfg *config.Config,
) {
	sessions := session.New([]byte(cfg.SessionKey))

	// Auth routes (HTML, no admin check)
	mux.Handle("GET /register", auth.HandleRegisterForm(logger))
	mux.Handle(
		"POST /register",
		auth.HandleRegisterSubmit(logger, stores.Players, sessions, cfg.AdminUsernames),
	)
	mux.Handle("GET /login", auth.HandleLoginForm(logger))
	mux.Handle("POST /login", auth.HandleLoginSubmit(logger, stores.Players, sessions))
	mux.Handle("POST /logout", auth.HandleLogout(sessions))

	// Admin interface routes (require admin role)
	requireAdmin := func(h http.Handler) http.Handler {
		return auth.RequireAdmin(h, stores.Players, sessions, logger)
	}

	mux.Handle("GET /admin", requireAdmin(admin.HandleIndex(logger)))
	mux.Handle("GET /admin/quizzes", requireAdmin(admin.HandleQuizList(logger, stores.Quizzes)))
	mux.Handle("GET /admin/quizzes/{quizID}", requireAdmin(admin.HandleQuizView(logger, stores.Quizzes)))
	mux.Handle("GET /admin/quizzes/new", requireAdmin(admin.HandleQuizCreate(logger)))
	mux.Handle("POST /admin/quizzes", requireAdmin(admin.HandleQuizSave(logger, stores.Quizzes)))
	mux.Handle("GET /admin/quizzes/{quizID}/edit", requireAdmin(admin.HandleQuizEdit(logger, stores.Quizzes)))
	mux.Handle("POST /admin/quizzes/{quizID}", requireAdmin(admin.HandleQuizSave(logger, stores.Quizzes)))
	mux.Handle("POST /admin/quizzes/{quizID}/delete", requireAdmin(admin.HandleQuizDelete(logger, stores.Quizzes)))

	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/new",
		requireAdmin(admin.HandleQuestionCreate(logger, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions",
		requireAdmin(admin.HandleQuestionSave(logger, stores.Quizzes)),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/{questionID}/edit",
		requireAdmin(admin.HandleQuestionEdit(logger, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}",
		requireAdmin(admin.HandleQuestionSave(logger, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/delete",
		requireAdmin(admin.HandleQuestionDelete(logger, stores.Quizzes)),
	)

	// API
	mux.Handle("GET /api/quizzes", clientapi.HandleQuizList(logger, stores.Quizzes))
	mux.Handle("GET /api/quizzes/{slugID}", clientapi.HandleQuizGet(logger, stores.Quizzes))
	mux.Handle("POST /api/games", clientapi.HandleCreateGame(logger, gameService))
	mux.Handle("GET /api/games/{gameID}/questions/next", clientapi.HandleQuestionNext(logger, gameService))
	mux.Handle(
		"POST /api/games/{gameID}/questions/{questionID}/answers",
		clientapi.HandleAnswerPost(logger, gameService),
	)
	mux.Handle("GET /api/games/{gameID}/results", clientapi.HandleGameResults(logger, gameService))

	// Client
	mux.Handle("/client/", client.Handler(cfg))

	// Health
	mux.Handle("GET /healthz", health.HandleHealthz(logger, stores))

	// Not found
	mux.Handle("/", http.NotFoundHandler())
}
