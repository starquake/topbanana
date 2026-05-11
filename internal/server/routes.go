package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/client"
	"github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/csrf"
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
	csrfMgr := csrf.New([]byte(cfg.SessionKey))

	addAuthRoutes(mux, logger, stores, sessions, csrfMgr, cfg)
	addAdminRoutes(mux, logger, stores, gameService, sessions, csrfMgr)
	addAPIRoutes(mux, logger, stores, gameService, sessions)

	// Client
	clientHandler := client.Handler(cfg)
	mux.Handle("/client/", clientHandler)

	// Per-quiz share URL. Serves the same SPA shell as /client/ but the
	// frontend reads the slug-id off the path and pre-selects the quiz.
	// Path is rewritten to /client/ so the existing file server (and its
	// production minification middleware) keeps doing the work.
	mux.Handle("GET /play/{slugID}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/client/"
		clientHandler.ServeHTTP(w, r2)
	}))

	// Health
	mux.Handle("GET /healthz", health.HandleHealthz(logger, stores))

	// Not found
	mux.Handle("/", http.NotFoundHandler())
}

// addAuthRoutes registers the unauthenticated auth-flow routes. Registration
// is only registered when REGISTRATION_ENABLED is true; when disabled,
// /register naturally 404s from the mux, which is the desired UX.
//
// The CSRF middleware guards every unsafe method; safe methods pass through so
// the GET form renderer can still set the nonce cookie.
func addAuthRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
) {
	csrfMW := csrfMgr.Middleware

	if cfg.RegistrationEnabled {
		mux.Handle("GET /register", auth.HandleRegisterForm(logger, csrfMgr))
		mux.Handle(
			"POST /register",
			csrfMW(auth.HandleRegisterSubmit(logger, csrfMgr, stores.Players, sessions, cfg.AdminUsernames)),
		)
	}
	mux.Handle("GET /login", auth.HandleLoginForm(logger, csrfMgr, cfg.RegistrationEnabled))
	mux.Handle(
		"POST /login",
		csrfMW(auth.HandleLoginSubmit(logger, csrfMgr, stores.Players, sessions, cfg.RegistrationEnabled)),
	)
	mux.Handle("POST /logout", csrfMW(auth.HandleLogout(sessions)))
}

// addAdminRoutes registers every /admin/* route. Each unsafe (POST/PUT/...)
// route is wrapped as csrfMW(requireAdmin(handler)): the CSRF middleware runs
// first so an unauthenticated request without a valid token is rejected with
// 403 before any auth-state-leaking 303 to /login.
func addAdminRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
) {
	csrfMW := csrfMgr.Middleware
	requireAdmin := func(h http.Handler) http.Handler {
		return auth.RequireAdmin(h, stores.Players, sessions, csrfMgr, logger)
	}

	mux.Handle("GET /admin", requireAdmin(admin.HandleIndex(logger, csrfMgr)))
	mux.Handle("GET /admin/quizzes", requireAdmin(admin.HandleQuizList(logger, csrfMgr, stores.Quizzes)))
	mux.Handle(
		"GET /admin/quizzes/{quizID}",
		requireAdmin(admin.HandleQuizView(logger, csrfMgr, stores.Quizzes, gameService)),
	)
	mux.Handle("GET /admin/quizzes/new", requireAdmin(admin.HandleQuizCreate(logger, csrfMgr)))
	mux.Handle("POST /admin/quizzes", csrfMW(requireAdmin(admin.HandleQuizSave(logger, csrfMgr, stores.Quizzes))))
	mux.Handle(
		"GET /admin/quizzes/{quizID}/edit",
		requireAdmin(admin.HandleQuizEdit(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}",
		csrfMW(requireAdmin(admin.HandleQuizSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/delete",
		csrfMW(requireAdmin(admin.HandleQuizDelete(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/players/{playerID}/reset",
		csrfMW(requireAdmin(admin.HandleResetGameForPlayer(logger, csrfMgr, gameService))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/new",
		requireAdmin(admin.HandleQuestionCreate(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions",
		csrfMW(requireAdmin(admin.HandleQuestionSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/{questionID}/edit",
		requireAdmin(admin.HandleQuestionEdit(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}",
		csrfMW(requireAdmin(admin.HandleQuestionSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/delete",
		csrfMW(requireAdmin(admin.HandleQuestionDelete(logger, csrfMgr, stores.Quizzes))),
	)
}

// addAPIRoutes registers the JSON API routes consumed by the game client.
// These do not need CSRF protection: they expect application/json bodies and
// do not rely on cookie auth, so the classic browser-form CSRF threat does
// not apply.
//
// Every route is wrapped in EnsurePlayer so a cookieless visitor is silently
// upgraded to an anonymous players row before the handler runs. This means
// HandleCreateGame and HandleAnswerPost can safely read the player off the
// request context. The static /client/* assets are intentionally not wrapped
// — loading the SPA shell should not create a row; the first /api/ call
// does.
func addAPIRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	sessions *session.Manager,
) {
	ensurePlayer := func(h http.Handler) http.Handler {
		return auth.EnsurePlayer(h, stores.Players, sessions, logger)
	}

	mux.Handle("GET /api/players/me", ensurePlayer(clientapi.HandlePlayerGetMe(logger)))
	mux.Handle(
		"PATCH /api/players/me",
		ensurePlayer(clientapi.HandlePlayerClaimName(logger, stores.Players)),
	)
	mux.Handle("GET /api/quizzes", ensurePlayer(clientapi.HandleQuizList(logger, stores.Quizzes)))
	mux.Handle("GET /api/quizzes/{slugID}", ensurePlayer(clientapi.HandleQuizGet(logger, stores.Quizzes)))
	mux.Handle(
		"GET /api/quizzes/{slugID}/leaderboard",
		ensurePlayer(clientapi.HandleQuizLeaderboard(logger, gameService)),
	)
	mux.Handle(
		"GET /api/quizzes/{slugID}/my-game",
		ensurePlayer(clientapi.HandleGameForQuiz(logger, gameService)),
	)
	mux.Handle("POST /api/games", ensurePlayer(clientapi.HandleCreateGame(logger, gameService)))
	mux.Handle(
		"GET /api/games/{gameID}/questions/next",
		ensurePlayer(clientapi.HandleQuestionNext(logger, gameService)),
	)
	mux.Handle(
		"POST /api/games/{gameID}/questions/{questionID}/answers",
		ensurePlayer(clientapi.HandleAnswerPost(logger, gameService)),
	)
	mux.Handle("GET /api/games/{gameID}/results", ensurePlayer(clientapi.HandleGameResults(logger, gameService)))
}
