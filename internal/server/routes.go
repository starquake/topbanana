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
	"github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/web"
)

func addRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	leaderboardHub *leaderboard.Hub,
	cfg *config.Config,
) {
	sessions := session.New([]byte(cfg.SessionKey), cfg.SecureCookies())
	csrfMgr := csrf.New([]byte(cfg.SessionKey), cfg.SecureCookies())

	addAuthRoutes(mux, logger, stores, sessions, csrfMgr, cfg)
	addAdminRoutes(mux, logger, stores, gameService, sessions, csrfMgr)
	addAPIRoutes(mux, logger, stores, gameService, leaderboardHub, sessions)

	// Client
	clientHandler := client.Handler(cfg)
	shell := client.NewShellHandlers(cfg, stores.Quizzes, logger)
	// The SPA root and the per-quiz share URL both go through the shell
	// handler so the index template can render Open Graph metadata. The
	// shell route wins over the file-server fallback below because Go's
	// mux picks the more specific pattern (`{$}` + method).
	mux.Handle("GET /client/{$}", http.HandlerFunc(shell.Index))
	mux.Handle("/client/", clientHandler)
	mux.Handle("GET /play/{slugID}", http.HandlerFunc(shell.Play))

	// Admin + auth static assets (Tailwind output, embedded in the binary).
	mux.Handle("/assets/", web.Handler(cfg))

	// Health
	mux.Handle("GET /healthz", health.HandleHealthz(logger, stores))

	// Public start page (#166). Registered as `GET /{$}` so only the exact
	// root matches; unknown paths fall through to Go's mux default 404.
	// The home pages share two per-request closures: viewerFunc resolves
	// the session's player for the "Signed in as X" footer (#408);
	// csrfTokenFunc seeds the log-out form so POST /logout passes the
	// CSRF middleware.
	viewerFunc := homeViewerFunc(stores.Players, sessions)
	csrfTokenFunc := home.CSRFTokenFunc(csrfMgr.Token)
	mux.Handle("GET /{$}", home.Handle(logger, stores.Home, viewerFunc, csrfTokenFunc))
	// Public quizzes list (#284). Lists every visible quiz so players
	// can find ones outside the home page's top-six popular slice.
	mux.Handle("GET /quizzes", home.HandleAllQuizzes(logger, stores.Quizzes, viewerFunc, csrfTokenFunc))
}

// homeViewerFunc returns a closure that resolves the signed-in player
// for the home-page footer affordance. Returns nil for anonymous
// sessions (or any lookup error) so the template falls back to the
// "Log in" link path.
func homeViewerFunc(players auth.PlayerStore, sessions *session.Manager) home.ViewerFunc {
	return func(r *http.Request) *home.Viewer {
		id, ok := sessions.PlayerID(r)
		if !ok {
			return nil
		}
		p, err := players.GetPlayerByID(r.Context(), id)
		if err != nil || !p.IsAuthenticated() {
			return nil
		}

		return &home.Viewer{Username: p.Username}
	}
}

// addAuthRoutes registers the unauthenticated auth-flow routes. Registration
// is only registered when REGISTRATION_ENABLED is true; when disabled,
// /register naturally 404s from the mux, which is the desired UX. The
// /login/google routes follow the same pattern: registered only when
// every Google OAuth env var is set, 404 otherwise.
//
// The CSRF middleware guards every unsafe method; safe methods pass through so
// the GET form renderer can still set the nonce cookie. The Google OAuth
// routes are intentionally GET-only (initial redirect + callback) and do
// not need the form CSRF middleware; the OAuth state parameter is the
// CSRF token for that flow.
func addAuthRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
) {
	csrfMW := csrfMgr.Middleware
	googleEnabled := cfg.GoogleLoginEnabled()

	if cfg.RegistrationEnabled {
		mux.Handle("GET /register", auth.HandleRegisterForm(logger, csrfMgr, stores.Players, sessions, googleEnabled))
		mux.Handle(
			"POST /register",
			csrfMW(auth.HandleRegisterSubmit(
				logger, csrfMgr, stores.Players, sessions, cfg.AdminUsernames, googleEnabled,
			)),
		)
	}
	mux.Handle(
		"GET /login",
		auth.HandleLoginForm(logger, csrfMgr, stores.Players, sessions, cfg.RegistrationEnabled, googleEnabled),
	)
	mux.Handle(
		"POST /login",
		csrfMW(auth.HandleLoginSubmit(
			logger, csrfMgr, stores.Players, sessions, cfg.RegistrationEnabled, googleEnabled,
		)),
	)
	mux.Handle("POST /logout", csrfMW(auth.HandleLogout(sessions)))

	if googleEnabled {
		googleAuth := auth.NewGoogleAuthenticator(auth.GoogleConfig{
			ClientID:      cfg.GoogleClientID,
			ClientSecret:  cfg.GoogleClientSecret,
			RedirectURL:   cfg.GoogleRedirectURL,
			IssuerURL:     cfg.GoogleIssuerURL,
			SecureCookies: cfg.SecureCookies(),
		}, []byte(cfg.SessionKey))
		mux.Handle("GET /login/google", auth.HandleGoogleLogin(logger, googleAuth))
		mux.Handle(
			"GET /login/google/callback",
			auth.HandleGoogleCallback(logger, googleAuth, csrfMgr, stores.OAuth, sessions, cfg.RegistrationEnabled),
		)
	} else {
		logger.Info(
			"google sign-in disabled (set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL to enable)",
		)
	}
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
	mux.Handle("GET /admin/quizzes/import", requireAdmin(admin.HandleQuizImportForm(logger, csrfMgr)))
	mux.Handle(
		"POST /admin/quizzes/import",
		csrfMW(requireAdmin(admin.HandleQuizImportSave(logger, csrfMgr, stores.Quizzes))),
	)
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
		csrfMW(requireAdmin(admin.HandleResetGameForPlayer(logger, csrfMgr, stores.Quizzes, gameService))),
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
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/move/{direction}",
		csrfMW(requireAdmin(admin.HandleQuestionMove(logger, csrfMgr, stores.Quizzes))),
	)
}

// addAPIRoutes registers the JSON API routes consumed by the game client.
// API routes use the same session cookie as the rest of the app. CSRF
// protection is provided entirely by SameSite=Lax on the session cookie
// (see internal/session/session.go). Removing or weakening SameSite
// requires adding an Origin / Sec-Fetch-Site check on unsafe methods.
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
	leaderboardHub *leaderboard.Hub,
	sessions *session.Manager,
) {
	ensurePlayer := func(h http.Handler) http.Handler {
		return auth.EnsurePlayer(h, stores.Players, sessions, logger)
	}

	mux.Handle("GET /api/players/me", ensurePlayer(clientapi.HandlePlayerGetMe(logger)))
	mux.Handle(
		"PATCH /api/players/me",
		ensurePlayer(clientapi.HandlePlayerClaimName(logger, stores.Players, gameService)),
	)
	mux.Handle("GET /api/quizzes", ensurePlayer(clientapi.HandleQuizList(logger, stores.Quizzes)))
	mux.Handle("GET /api/quizzes/{slugID}", ensurePlayer(clientapi.HandleQuizGet(logger, stores.Quizzes)))
	mux.Handle(
		"GET /api/quizzes/{slugID}/leaderboard",
		ensurePlayer(clientapi.HandleQuizLeaderboard(logger, gameService)),
	)
	mux.Handle(
		"GET /api/quizzes/{slugID}/leaderboard/stream",
		ensurePlayer(clientapi.HandleQuizLeaderboardStream(logger, gameService, leaderboardHub)),
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
