package server

import (
	"log/slog"
	"net"
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
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/profile"
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
	mailerTester *mailer.Tester,
	mailerStatus mailer.StatusView,
) {
	sessions := session.New([]byte(cfg.SessionKey), cfg.SecureCookies())
	csrfMgr := csrf.New([]byte(cfg.SessionKey), cfg.SecureCookies())

	emailDeps := adminEmailDeps{
		tester:            mailerTester,
		status:            mailerStatus,
		flash:             admin.NewEmailFlash([]byte(cfg.SessionKey), cfg.SecureCookies()),
		trustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}
	playerDeps := adminPlayerDeps{
		tokens: stores.VerifyTokens,
		sender: mailerTester,
		flash: auth.NewSignedFlash(
			[]byte(cfg.SessionKey), cfg.SecureCookies(),
			admin.PlayerDetailFlashCookieName, admin.PlayerDetailFlashCookiePath,
		),
		inviteFlash: auth.NewSignedFlash(
			[]byte(cfg.SessionKey), cfg.SecureCookies(),
			admin.InviteFlashCookieName, admin.InviteFlashCookiePath,
		),
		baseURL: cfg.BaseURL,
	}

	addAuthRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mailerTester)
	addAdminRoutes(mux, logger, stores, gameService, sessions, csrfMgr, emailDeps, playerDeps)
	addProfileRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mailerTester)
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

	// PWA manifest + service worker. Both live at the site root so the
	// install prompt and the SW's default scope cover every page.
	mux.Handle("GET /manifest.webmanifest", web.ManifestHandler(cfg))
	mux.Handle("GET /sw.js", web.ServiceWorkerHandler(cfg))

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

// addEmailFlowRoutes registers the verify-email and forgot-password
// routes. Extracted from addAuthRoutes so each function stays under
// the revive function-length cap; the two flows share the same
// limiter type and the same MaxFormSizeMiddleware wrapper pattern,
// so they live together.
func addEmailFlowRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
	mailerTester *mailer.Tester,
) {
	csrfMW := csrfMgr.Middleware

	mux.Handle("GET /verify-email", auth.HandleVerifyEmail(
		logger, csrfMgr, stores.VerifyTokens, stores.Players, sessions,
	))

	verifyFlash := auth.NewSignedFlash(
		[]byte(cfg.SessionKey), cfg.SecureCookies(),
		auth.VerifyFlashCookieName, auth.VerifyFlashCookiePath,
	)
	// Two VerifyResendLimiter instances on purpose: a stampede on the
	// in-session resend must not throttle the public self-service form,
	// and vice versa. Both share the same window via VerifyResendCooldown.
	resendLimiter := auth.NewVerifyResendLimiter(auth.VerifyResendCooldown(), cfg.TrustedProxyCIDRs)
	mux.Handle("GET /verify-email/pending", auth.HandleVerifyPending(
		logger, csrfMgr, stores.Players, sessions, verifyFlash,
	))
	mux.Handle("POST /verify-email/resend", admin.MaxFormSizeMiddleware(csrfMW(auth.HandleVerifyResend(
		logger, stores.Players, sessions, stores.VerifyTokens, mailerTester,
		cfg.BaseURL, resendLimiter, verifyFlash,
	))))

	verifyRequestFlash := auth.NewSignedFlash(
		[]byte(cfg.SessionKey), cfg.SecureCookies(),
		auth.VerifyRequestFlashCookieName, auth.VerifyRequestFlashCookiePath,
	)
	verifyRequestLimiter := auth.NewVerifyResendLimiter(auth.VerifyResendCooldown(), cfg.TrustedProxyCIDRs)
	mux.Handle("GET /verify-email/request", auth.HandleVerifyEmailRequestForm(
		logger, csrfMgr, stores.Players, sessions, verifyRequestFlash,
	))
	mux.Handle("POST /verify-email/request", admin.MaxFormSizeMiddleware(
		csrfMW(auth.HandleVerifyEmailRequestSubmit(
			logger, stores.Players, sessions, stores.VerifyTokens, mailerTester,
			cfg.BaseURL, verifyRequestLimiter, verifyRequestFlash,
		)),
	))

	forgotFlash := auth.NewSignedFlash(
		[]byte(cfg.SessionKey), cfg.SecureCookies(),
		auth.ForgotFlashCookieName, auth.ForgotFlashCookiePath,
	)
	forgotLimiter := auth.NewVerifyResendLimiter(auth.ForgotPasswordCooldown(), cfg.TrustedProxyCIDRs)
	mux.Handle("GET /forgot-password", auth.HandleForgotForm(
		logger, csrfMgr, stores.Players, sessions, forgotFlash,
	))
	mux.Handle("POST /forgot-password", admin.MaxFormSizeMiddleware(csrfMW(auth.HandleForgotSubmit(
		logger, stores.Players, sessions, stores.ResetTokens, mailerTester,
		cfg.BaseURL, forgotLimiter, forgotFlash,
	))))

	mux.Handle("GET /reset-password", auth.HandleResetForm(logger, csrfMgr, stores.ResetTokens))
	mux.Handle("POST /reset-password", admin.MaxFormSizeMiddleware(csrfMW(
		auth.HandleResetSubmit(logger, csrfMgr, stores.ResetTokens, sessions, stores.Players),
	)))

	mux.Handle("GET /accept-invite", auth.HandleAcceptInviteForm(logger, csrfMgr, stores.Invites))
	mux.Handle("POST /accept-invite", admin.MaxFormSizeMiddleware(csrfMW(
		auth.HandleAcceptInviteSubmit(logger, csrfMgr, auth.AcceptInviteDeps{
			Invites:  stores.Invites,
			Players:  stores.InvitePlayers,
			Sessions: sessions,
		}),
	)))
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

		return &home.Viewer{DisplayName: p.DisplayName}
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
	mailerTester *mailer.Tester,
) {
	csrfMW := csrfMgr.Middleware
	googleEnabled := cfg.GoogleLoginEnabled()

	if cfg.RegistrationEnabled {
		mux.Handle("GET /register", auth.HandleRegisterForm(logger, csrfMgr, stores.Players, sessions, googleEnabled))
		mux.Handle(
			"POST /register",
			csrfMW(auth.HandleRegisterSubmit(
				logger, csrfMgr, stores.Players, sessions,
				auth.RegisterDeps{
					AdminEmails:   cfg.AdminEmails,
					GoogleEnabled: googleEnabled,
					Mailer:        mailerTester,
					Tokens:        stores.VerifyTokens,
					BaseURL:       cfg.BaseURL,
				},
			)),
		)
	}
	loginLimiter := auth.NewLoginRateLimiter(cfg.LoginCooldown, cfg.TrustedProxyCIDRs)
	// loginResendLimiter is a dedicated per-IP cooldown for the
	// verify-email send the login handler issues on an unverified-but-
	// correct credential attempt (#492). Separate from the resend
	// limiter on the verify-email/pending form so a stampede on one
	// path cannot starve the other.
	loginResendLimiter := auth.NewVerifyResendLimiter(auth.VerifyResendCooldown(), cfg.TrustedProxyCIDRs)
	mux.Handle(
		"GET /login",
		auth.HandleLoginForm(logger, csrfMgr, stores.Players, sessions, cfg.RegistrationEnabled, googleEnabled),
	)
	mux.Handle(
		"POST /login",
		csrfMW(auth.HandleLoginSubmit(
			logger, csrfMgr, auth.LoginDeps{
				Players:             stores.Players,
				Sessions:            sessions,
				Games:               stores.GameMigrator,
				Limiter:             loginLimiter,
				Mailer:              mailerTester,
				Tokens:              stores.VerifyTokens,
				ResendLimiter:       loginResendLimiter,
				BaseURL:             cfg.BaseURL,
				RegistrationEnabled: cfg.RegistrationEnabled,
				GoogleEnabled:       googleEnabled,
			},
		)),
	)
	mux.Handle("POST /logout", csrfMW(auth.HandleLogout(sessions)))

	addEmailFlowRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mailerTester)

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
			auth.HandleGoogleCallback(
				logger, googleAuth, csrfMgr, stores.OAuth, stores.Players, sessions, stores.GameMigrator,
				cfg.RegistrationEnabled,
			),
		)
	} else {
		logger.Info(
			"google sign-in disabled (set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL to enable)",
		)
	}
}

// addProfileRoutes registers the per-player profile page (#410) and
// its sibling controls (#497 email change). Every route is wrapped
// in RequireAuthenticated + RequireVerifiedEmail so an anonymous or
// unverified session is bounced before the handler runs; POST routes
// additionally pass through csrfMW.
//
// MaxFormSizeMiddleware fronts the email-change POST in front of
// csrfMW so the CSRF validator's ParseForm sees a bounded body. The
// rest of the profile POSTs already cap the body in-handler via
// [http.MaxBytesReader].
func addProfileRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
	sender auth.VerifyEmailSender,
) {
	csrfMW := csrfMgr.Middleware
	requireAuthn := func(h http.Handler) http.Handler {
		return auth.RequireAuthenticated(auth.RequireVerifiedEmail(h), stores.Players, sessions, logger)
	}

	mux.Handle("GET /profile", requireAuthn(profile.HandleProfile(logger, csrfMgr)))
	mux.Handle(
		"POST /profile/display-name",
		csrfMW(requireAuthn(profile.HandleProfileDisplayName(logger, csrfMgr, stores.Players))),
	)
	mux.Handle("GET /profile/password", requireAuthn(profile.HandleProfilePassword(logger, csrfMgr)))
	mux.Handle(
		"POST /profile/password",
		csrfMW(requireAuthn(profile.HandleProfilePasswordChange(logger, csrfMgr, stores.Players, sessions))),
	)

	emailFlash := auth.NewSignedFlash(
		[]byte(cfg.SessionKey), cfg.SecureCookies(),
		profile.EmailChangeFlashCookieName, profile.EmailChangeFlashCookiePath,
	)
	mux.Handle(
		"GET /profile/email",
		requireAuthn(profile.HandleProfileEmail(logger, csrfMgr, emailFlash)),
	)
	mux.Handle(
		"POST /profile/email",
		admin.MaxFormSizeMiddleware(
			csrfMW(requireAuthn(profile.HandleProfileEmailChange(logger, profile.EmailChangeDeps{
				Players: stores.Players,
				Tokens:  stores.VerifyTokens,
				Sender:  sender,
				Flash:   emailFlash,
				BaseURL: cfg.BaseURL,
			}))),
		),
	)
}

// addAdminRoutes registers every /admin/* route. Each unsafe (POST/PUT/...)
// route is wrapped as csrfMW(requireAdmin(handler)): the CSRF middleware runs
// first so an unauthenticated request without a valid token is rejected with
// 403 before any auth-state-leaking 303 to /login.
// adminEmailDeps bundles the email-diagnostics handler deps so
// addAdminRoutes stays inside revive's 8-argument limit.
type adminEmailDeps struct {
	tester            *mailer.Tester
	status            mailer.StatusView
	flash             *admin.EmailFlash
	trustedProxyCIDRs []*net.IPNet
}

// adminPlayerDeps bundles the admin player-management deps (#450).
// Same packaging reason as adminEmailDeps: the management routes share
// a flash, a token store, a mailer, and a base URL, and bundling them
// keeps addAdminRoutes under revive's argument cap.
type adminPlayerDeps struct {
	tokens auth.VerifyTokenStore
	sender auth.VerifyEmailSender
	flash  *auth.SignedFlash
	// inviteFlash is the one-shot banner for the invite management page
	// (#318); scoped to its own cookie path so it does not collide with
	// the player-detail flash.
	inviteFlash *auth.SignedFlash
	baseURL     string
}

func addAdminRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	email adminEmailDeps,
	playerDeps adminPlayerDeps,
) {
	csrfMW := csrfMgr.Middleware
	// requireGameHost gates the dashboard + quiz/round routes to Hosts and
	// Admins (#538). A signed-in Player gets a 403 access-denied page (the
	// dashboard's existence is not secret).
	requireGameHost := func(h http.Handler) http.Handler {
		return auth.RequireGameHost(auth.RequireVerifiedEmail(h), stores.Players, sessions, csrfMgr, logger)
	}
	// requireAdmin gates the top-tier-only routes (#538): player management,
	// role changes, account creation, email diagnostics, and settings. A
	// signed-in non-Admin (Player or Host) gets a 404 from RequireAdmin so the
	// route's existence stays hidden (#320/#538); the verified-email gate sits
	// inside it for parity with requireGameHost.
	requireAdmin := func(h http.Handler) http.Handler {
		return auth.RequireAdmin(auth.RequireVerifiedEmail(h), stores.Players, sessions, logger)
	}

	mux.Handle("GET /admin", requireGameHost(admin.HandleIndex(logger, csrfMgr)))
	addAdminSettingsRoutes(mux, logger, csrfMgr, requireAdmin, stores, playerDeps)
	mux.Handle("GET /admin/players", requireAdmin(admin.HandlePlayersList(logger, csrfMgr, stores.PlayerLister)))
	addAdminPlayerRoutes(mux, logger, csrfMgr, csrfMW, requireAdmin, stores, playerDeps)
	addAdminEmailRoutes(mux, logger, csrfMgr, csrfMW, requireAdmin, email)
	mux.Handle("GET /admin/quizzes", requireGameHost(admin.HandleQuizList(logger, csrfMgr, stores.Quizzes)))
	mux.Handle(
		"GET /admin/quizzes/{quizID}",
		requireGameHost(admin.HandleQuizView(logger, csrfMgr, stores.Quizzes, gameService)),
	)
	mux.Handle("GET /admin/quizzes/new", requireGameHost(admin.HandleQuizCreate(logger, csrfMgr)))
	mux.Handle("POST /admin/quizzes", csrfMW(requireGameHost(admin.HandleQuizSave(logger, csrfMgr, stores.Quizzes))))
	mux.Handle("GET /admin/quizzes/import", requireGameHost(admin.HandleQuizImportForm(logger, csrfMgr)))
	mux.Handle(
		"POST /admin/quizzes/import",
		csrfMW(requireGameHost(admin.HandleQuizImportSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/edit",
		requireGameHost(admin.HandleQuizEdit(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}",
		csrfMW(requireGameHost(admin.HandleQuizSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/delete",
		csrfMW(requireGameHost(admin.HandleQuizDelete(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/players/{playerID}/reset",
		csrfMW(requireGameHost(admin.HandleResetGameForPlayer(logger, csrfMgr, stores.Quizzes, gameService))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/new",
		requireGameHost(admin.HandleQuestionCreate(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions",
		csrfMW(requireGameHost(admin.HandleQuestionSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/questions/{questionID}/edit",
		requireGameHost(admin.HandleQuestionEdit(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}",
		csrfMW(requireGameHost(admin.HandleQuestionSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/delete",
		csrfMW(requireGameHost(admin.HandleQuestionDelete(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/move/{direction}",
		csrfMW(requireGameHost(admin.HandleQuestionMove(logger, csrfMgr, stores.Quizzes))),
	)

	addAdminRoundRoutes(mux, logger, stores, csrfMW, requireGameHost, csrfMgr)
}

// addAdminSettingsRoutes registers the Admin settings page (#320/#538): the
// GET render of the current Admins list. The page's demote buttons post to the
// id-based role endpoint under /admin/players (#538), so there is no
// settings-scoped POST here. Gated by requireAdmin so a signed-in non-Admin
// gets a 404 (the route stays hidden).
func addAdminSettingsRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	requireAdmin func(http.Handler) http.Handler,
	stores *store.Stores,
	deps adminPlayerDeps,
) {
	mux.Handle(
		"GET /admin/settings",
		requireAdmin(admin.HandleSettings(logger, csrfMgr, stores.AdminList, deps.flash)),
	)
}

// addAdminPlayerRoutes registers the admin player-management routes (#450).
// Every route - the per-player detail view, the verify/resend/email actions,
// the create-without-verification pair, the id-based role endpoint (#538), and
// the displayName + password actions (#535) - is Admin-only (#538): player
// management moved from the old admin-wide gate up to the top tier.
// MaxFormSizeMiddleware fronts every POST in front of csrfMW so the CSRF
// validator's ParseForm sees a bounded body; csrfMW fronts the auth wrapper so
// an unauthenticated request without a valid token is rejected with 403 before
// any auth-state-leaking 303 to /login.
func addAdminPlayerRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	csrfMW func(http.Handler) http.Handler,
	requireAdmin func(http.Handler) http.Handler,
	stores *store.Stores,
	deps adminPlayerDeps,
) {
	resendLimiter := admin.NewPerTargetLimiter(admin.AdminResendVerificationCooldown)

	mux.Handle(
		"GET /admin/players/new",
		requireAdmin(admin.HandlePlayerCreateForm(logger, csrfMgr)),
	)
	mux.Handle(
		"POST /admin/players",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerCreateSubmit(logger, csrfMgr, stores.AdminPlayers, deps.flash),
		))),
	)
	mux.Handle(
		"GET /admin/players/{playerID}",
		requireAdmin(admin.HandlePlayerDetail(logger, csrfMgr, stores.AdminPlayers, deps.flash)),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/verify",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerMarkVerified(logger, stores.AdminPlayers, deps.flash),
		))),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/resend-verification",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerResendVerification(
				logger, stores.AdminPlayers, deps.tokens, deps.sender,
				deps.baseURL, resendLimiter, deps.flash,
			),
		))),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/email",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerSetEmail(logger, stores.AdminPlayers, deps.flash),
		))),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/role",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerSetRole(logger, stores.AdminPlayers, deps.flash),
		))),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/display-name",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerSetDisplayName(logger, stores.AdminPlayers, deps.flash),
		))),
	)
	mux.Handle(
		"POST /admin/players/{playerID}/password",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandlePlayerSetPassword(logger, stores.AdminPlayers, deps.flash),
		))),
	)
	addAdminInviteRoutes(mux, logger, csrfMgr, csrfMW, requireAdmin, stores, deps)
}

// addAdminInviteRoutes registers the admin invite management routes (#318):
// the canonical /admin/invites page (pending list + create form), the
// create POST, and the per-row resend / revoke POST actions. A legacy
// GET /admin/invites/new 301-redirects to the canonical page so any
// bookmarked slice-1 link still resolves. Admin-only, like the other
// player-management routes. MaxFormSizeMiddleware fronts every POST in
// front of csrfMW so the CSRF validator's ParseForm sees a bounded body;
// csrfMW fronts the auth wrapper so an unauthenticated request without a
// valid token is rejected with 403 before any auth-state-leaking 303 to
// /login.
func addAdminInviteRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	csrfMW func(http.Handler) http.Handler,
	requireAdmin func(http.Handler) http.Handler,
	stores *store.Stores,
	deps adminPlayerDeps,
) {
	inviteDeps := admin.InviteDeps{
		Players: stores.Players,
		Invites: stores.Invites,
		Sender:  deps.sender,
		Flash:   deps.inviteFlash,
		BaseURL: deps.baseURL,
	}
	mux.Handle(
		"GET /admin/invites",
		requireAdmin(admin.HandleInvitesPage(logger, csrfMgr, inviteDeps)),
	)
	mux.Handle(
		"GET /admin/invites/new",
		requireAdmin(admin.HandleInviteRedirect()),
	)
	mux.Handle(
		"POST /admin/invites",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandleInviteSubmit(logger, csrfMgr, inviteDeps),
		))),
	)
	mux.Handle(
		"POST /admin/invites/{id}/resend",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandleInviteResend(logger, inviteDeps),
		))),
	)
	mux.Handle(
		"POST /admin/invites/{id}/revoke",
		admin.MaxFormSizeMiddleware(csrfMW(requireAdmin(
			admin.HandleInviteRevoke(logger, inviteDeps),
		))),
	)
}

// addAdminEmailRoutes registers the email diagnostics endpoints (#321).
// One handler per (method, path) pair: GET renders status + ring buffer;
// POST sends a test message; GET on the POST path redirects to
// /admin/email so a browser refresh after a send does not 405. The
// limiter is created once so the per-IP cool-down is process-wide,
// not per-request.
//
// MaxFormSizeMiddleware wraps the POST in front of csrfMW so the CSRF
// validator's ParseForm sees a bounded body - the CSRF layer would
// otherwise read an unbounded request into memory before the handler
// could intervene.
func addAdminEmailRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	csrfMW func(http.Handler) http.Handler,
	requireAdmin func(http.Handler) http.Handler,
	email adminEmailDeps,
) {
	emailLimiter := admin.NewEmailRateLimiter(admin.EmailTestRateLimit, email.trustedProxyCIDRs)
	mux.Handle(
		"GET /admin/email",
		requireAdmin(admin.HandleEmailGet(logger, csrfMgr, email.tester, email.status, email.flash)),
	)
	mux.Handle(
		"GET /admin/email/test",
		requireAdmin(admin.HandleEmailTestRefresh()),
	)
	mux.Handle(
		"POST /admin/email/test",
		admin.MaxFormSizeMiddleware(
			csrfMW(requireAdmin(admin.HandleEmailTest(logger, email.tester, emailLimiter, email.flash))),
		),
	)
}

// addAdminRoundRoutes registers the round CRUD routes
// (#444). Split out of addAdminRoutes so that function stays under
// revive's function-length limit; the rounds block is otherwise
// structurally identical to the questions block above. The
// move-question-into-round route lets a host reassign a question to a
// different round.
func addAdminRoundRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	csrfMW func(http.Handler) http.Handler,
	requireGameHost func(http.Handler) http.Handler,
	csrfMgr *csrf.Manager,
) {
	mux.Handle(
		"GET /admin/quizzes/{quizID}/rounds/new",
		requireGameHost(admin.HandleRoundCreate(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/rounds",
		csrfMW(requireGameHost(admin.HandleRoundSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"GET /admin/quizzes/{quizID}/rounds/{roundID}/edit",
		requireGameHost(admin.HandleRoundEdit(logger, csrfMgr, stores.Quizzes)),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/rounds/{roundID}",
		csrfMW(requireGameHost(admin.HandleRoundSave(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/rounds/{roundID}/delete",
		csrfMW(requireGameHost(admin.HandleRoundDelete(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/rounds/{roundID}/move/{direction}",
		csrfMW(requireGameHost(admin.HandleRoundMove(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/round",
		csrfMW(requireGameHost(admin.HandleQuestionMoveToRound(logger, csrfMgr, stores.Quizzes))),
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
// - loading the SPA shell should not create a row; the first /api/ call
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
	mux.Handle(
		"POST /api/games/{gameID}/rounds/{roundID}/seen/{phase}",
		ensurePlayer(clientapi.HandleRoundSeen(logger, gameService)),
	)
	mux.Handle("GET /api/games/{gameID}/results", ensurePlayer(clientapi.HandleGameResults(logger, gameService)))
}
