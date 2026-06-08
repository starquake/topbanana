package server

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/client"
	"github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/host"
	"github.com/starquake/topbanana/internal/livesession"
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
	realtime Realtime,
	cfg *config.Config,
	mail Mail,
) {
	sessions := session.New([]byte(cfg.SessionKey), cfg.SecureCookies())
	csrfMgr := csrf.New([]byte(cfg.SessionKey), cfg.SecureCookies())

	emailDeps := adminEmailDeps{
		tester:            mail.Tester,
		status:            mail.Status,
		flash:             admin.NewEmailFlash([]byte(cfg.SessionKey), cfg.SecureCookies()),
		trustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}
	playerDeps := adminPlayerDeps{
		tokens: stores.VerifyTokens,
		sender: mail.Tester,
		flash: auth.NewSignedFlash(
			[]byte(cfg.SessionKey), cfg.SecureCookies(),
			admin.PlayerDetailFlashCookieName, admin.PlayerDetailFlashCookiePath,
		),
		inviteFlash: auth.NewSignedFlash(
			[]byte(cfg.SessionKey), cfg.SecureCookies(),
			admin.InviteFlashCookieName, admin.InviteFlashCookiePath,
		),
		baseURL:        cfg.BaseURL,
		mailConfigured: mail.Status.Configured,
		tasks:          mail.Tasks,
	}

	addAuthRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mail)
	addAdminRoutes(mux, logger, stores, gameService, sessions, csrfMgr, emailDeps, playerDeps)
	addProfileRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mail)
	addAPIRoutes(mux, logger, stores, gameService, realtime, sessions, cfg)
	addHostRoutes(mux, logger, stores, sessions, csrfMgr, realtime.SessionService)

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
	// Player join + lobby surface (MP-4 / #681). The bare /join is the PC
	// enter-code entry; /join/{code} is the QR deep-link target. Both render
	// the same join.html shell; the room code is read from the URL
	// client-side, so the shell carries no per-session data.
	mux.Handle("GET /join/{$}", http.HandlerFunc(shell.Join))
	mux.Handle("GET /join/{code}", http.HandlerFunc(shell.Join))

	// Admin + auth static assets (Tailwind output, embedded in the binary).
	mux.Handle("/assets/", web.Handler(cfg))

	// PWA manifest + service worker. Both live at the site root so the
	// install prompt and the SW's default scope cover every page.
	mux.Handle("GET /manifest.webmanifest", web.ManifestHandler(cfg))
	mux.Handle("GET /sw.js", web.ServiceWorkerHandler(cfg))

	// Health
	mux.Handle("GET /healthz", health.HandleHealthz(logger, stores))

	// Build stamp (#663). Public + side-effect free so uptime checks and
	// humans can read which release + commit is live without auth.
	mux.Handle("GET /version", health.HandleVersion(logger))

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
	mail Mail,
) {
	csrfMW := csrfMgr.Middleware

	mux.Handle("GET /verify-email", auth.HandleVerifyEmail(
		logger, csrfMgr, stores.VerifyTokens, stores.Players, stores.AdminPlayers, sessions, cfg.AdminEmails,
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
		logger, stores.Players, sessions, stores.VerifyTokens, mail.Tester,
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
			logger, stores.Players, sessions,
			auth.VerifyRequestDispatchDeps{
				Tokens:  stores.VerifyTokens,
				Sender:  mail.Tester,
				BaseURL: cfg.BaseURL,
				Tasks:   mail.Tasks,
			},
			verifyRequestLimiter, verifyRequestFlash,
		)),
	))

	addPasswordResetRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mail)

	mux.Handle("GET /accept-invite", auth.HandleAcceptInviteForm(logger, csrfMgr, stores.Invites))
	mux.Handle("POST /accept-invite", admin.MaxFormSizeMiddleware(csrfMW(
		auth.HandleAcceptInviteSubmit(logger, csrfMgr, auth.AcceptInviteDeps{
			Invites:  stores.Invites,
			Players:  stores.InvitePlayers,
			Sessions: sessions,
			Games:    stores.GameMigrator,
		}),
	)))
}

// addPasswordResetRoutes registers the forgot-password + reset-password pair.
// Split out of addEmailFlowRoutes so that function stays under revive's
// function-length cap; the forgot flow's detached reset-email dispatch is
// bundled into ForgotDispatchDeps so a graceful shutdown drains it before the
// DB closes (#740).
func addPasswordResetRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
	mail Mail,
) {
	csrfMW := csrfMgr.Middleware
	forgotFlash := auth.NewSignedFlash(
		[]byte(cfg.SessionKey), cfg.SecureCookies(),
		auth.ForgotFlashCookieName, auth.ForgotFlashCookiePath,
	)
	forgotLimiter := auth.NewVerifyResendLimiter(auth.ForgotPasswordCooldown(), cfg.TrustedProxyCIDRs)
	mux.Handle("GET /forgot-password", auth.HandleForgotForm(
		logger, csrfMgr, stores.Players, sessions, forgotFlash,
	))
	mux.Handle("POST /forgot-password", admin.MaxFormSizeMiddleware(csrfMW(auth.HandleForgotSubmit(
		logger, stores.Players, sessions,
		auth.ForgotDispatchDeps{
			Tokens:  stores.ResetTokens,
			Sender:  mail.Tester,
			BaseURL: cfg.BaseURL,
			Tasks:   mail.Tasks,
		},
		forgotLimiter, forgotFlash,
	))))

	mux.Handle("GET /reset-password", auth.HandleResetForm(logger, csrfMgr, stores.ResetTokens))
	mux.Handle("POST /reset-password", admin.MaxFormSizeMiddleware(csrfMW(
		auth.HandleResetSubmit(logger, csrfMgr, stores.ResetTokens, sessions, stores.Players),
	)))
}

// homeViewerFunc returns a closure that resolves the signed-in player
// for the home-page footer affordance. Returns nil for anonymous
// sessions (or any lookup error) so the template falls back to the
// "Log in" link path.
func homeViewerFunc(players auth.PlayerStore, sessions *session.Manager) home.ViewerFunc {
	return func(r *http.Request) *home.Viewer {
		p, ok := auth.AuthenticatedSessionPlayer(r, players, sessions)
		if !ok {
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
	mail Mail,
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
					GoogleEnabled: googleEnabled,
					Mailer:        mail.Tester,
					Tokens:        stores.VerifyTokens,
					BaseURL:       cfg.BaseURL,
					Tasks:         mail.Tasks,
				},
			)),
		)
	}
	addLoginRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mail, googleEnabled)

	addEmailFlowRoutes(mux, logger, stores, sessions, csrfMgr, cfg, mail)

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
				logger, googleAuth, csrfMgr, stores.OAuth, stores.Players, stores.AdminPlayers, sessions,
				stores.GameMigrator, cfg.AdminEmails, cfg.RegistrationEnabled,
			),
		)
	} else {
		logger.Info(
			"google sign-in disabled (set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL to enable)",
		)
	}
}

// addLoginRoutes registers GET/POST /login and POST /logout with the
// two-tier login throttle (#494/#786). Split out of addAuthRoutes so
// that function stays under revive's function-length cap. loginLimiter
// is the per-IP gap; accountLoginLimiter is the per-account backoff that
// the per-IP limiter cannot provide once an attacker rotates source
// addresses against one account. loginResendLimiter is a dedicated per-IP
// cooldown for the verify-email send the login handler issues on an
// unverified-but-correct attempt (#492), separate from the
// verify-email/pending resend so a stampede on one path cannot starve
// the other.
func addLoginRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	cfg *config.Config,
	mail Mail,
	googleEnabled bool,
) {
	csrfMW := csrfMgr.Middleware
	loginLimiter := auth.NewLoginRateLimiter(cfg.LoginCooldown, cfg.TrustedProxyCIDRs)
	accountLoginLimiter := auth.NewAccountLoginLimiter(auth.AccountLoginThreshold(), auth.AccountLoginCooldown())
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
				AccountLimiter:      accountLoginLimiter,
				Mailer:              mail.Tester,
				Tokens:              stores.VerifyTokens,
				ResendLimiter:       loginResendLimiter,
				BaseURL:             cfg.BaseURL,
				RegistrationEnabled: cfg.RegistrationEnabled,
				GoogleEnabled:       googleEnabled,
				Tasks:               mail.Tasks,
			},
		)),
	)
	mux.Handle("POST /logout", csrfMW(auth.HandleLogout(sessions)))
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
	mail Mail,
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
				Sender:  mail.Tester,
				Flash:   emailFlash,
				BaseURL: cfg.BaseURL,
				Tasks:   mail.Tasks,
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
	// mailConfigured reports whether SMTP is wired, so the role-change
	// handler only claims a notification email was sent when one could
	// actually leave the box.
	mailConfigured bool
	// tasks tracks the detached resend / role-change-notice dispatches so a
	// graceful shutdown drains them before the DB closes (#740).
	tasks *bgtasks.Tracker
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
		"POST /admin/quizzes/{quizID}/mode/{mode}",
		csrfMW(requireGameHost(admin.HandleQuizSetMode(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/delete",
		csrfMW(requireGameHost(admin.HandleQuizDelete(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/players/{playerID}/reset",
		csrfMW(requireGameHost(admin.HandleResetGameForPlayer(logger, csrfMgr, stores.Quizzes, gameService))),
	)

	addAdminQuestionRoutes(mux, logger, stores, csrfMW, requireGameHost, csrfMgr)
	addAdminRoundRoutes(mux, logger, stores, csrfMW, requireGameHost, csrfMgr)
}

// addAdminQuestionRoutes registers the question CRUD + reorder routes
// (#16). Split out of addAdminRoutes so that function stays under revive's
// function-length cap; the block is structurally identical to the rounds
// block in addAdminRoundRoutes.
func addAdminQuestionRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	csrfMW func(http.Handler) http.Handler,
	requireGameHost func(http.Handler) http.Handler,
	csrfMgr *csrf.Manager,
) {
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
				deps.baseURL, resendLimiter, deps.flash, deps.tasks,
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
			admin.HandlePlayerSetRole(
				logger, stores.AdminPlayers, deps.sender, deps.mailConfigured, deps.flash, deps.tasks,
			),
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
		"POST /admin/quizzes/{quizID}/rounds/{roundID}/position",
		csrfMW(requireGameHost(admin.HandleRoundPosition(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/round",
		csrfMW(requireGameHost(admin.HandleQuestionMoveToRound(logger, csrfMgr, stores.Quizzes))),
	)
	mux.Handle(
		"POST /admin/quizzes/{quizID}/questions/{questionID}/position",
		csrfMW(requireGameHost(admin.HandleQuestionPosition(logger, csrfMgr, stores.Quizzes))),
	)
}

// addAPIRoutes registers the JSON API routes consumed by the game client.
// API routes use the same session cookie as the rest of the app. CSRF
// protection has two layers: SameSite=Lax on the session cookie (see
// internal/session/session.go) and a same-origin guard on unsafe methods
// (sameOriginCheck) that rejects a cross-site Origin / Sec-Fetch-Site.
//
// Every route is wrapped in EnsurePlayer so a cookieless visitor is silently
// upgraded to an anonymous players row before the handler runs. This means
// HandleCreateGame and HandleAnswerPost can safely read the player off the
// request context. The same-origin guard runs outermost so a cross-site
// mutating request is rejected before any players row is minted. The static
// /client/* assets are intentionally not wrapped - loading the SPA shell
// should not create a row; the first /api/ call does.
func addAPIRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	gameService *game.Service,
	realtime Realtime,
	sessions *session.Manager,
	cfg *config.Config,
) {
	expectedOrigin := originFromBaseURL(cfg.BaseURL)
	ensurePlayer := func(h http.Handler) http.Handler {
		return sameOriginCheck(expectedOrigin, auth.EnsurePlayer(h, stores.Players, sessions, logger))
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
		ensurePlayer(clientapi.HandleQuizLeaderboardStream(logger, gameService, realtime.LeaderboardHub)),
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

	addSessionRoutes(mux, logger, realtime.SessionService, realtime.SessionHub, ensurePlayer)
}

// addSessionRoutes registers the hosted live-session API (MP-1 / #678,
// MP-2 / #679, MP-5 / #682). The service and its event hub are built in
// app.Run (the runner goroutine needs the same instances and the shutdown
// context), so they are threaded in here. The hub is the process-local
// pub/sub for the SSE event channel; the service and runner publish a tick on
// every transition and the events handler subscribes. Every route is wrapped
// in ensurePlayer so create (host gate, in-handler), join, ready, start,
// answer, state, and events all see a players row on the context; the host
// gate and participant gates live in the handlers and service.
func addSessionRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	sessionService *livesession.Service,
	sessionHub *livesession.Hub,
	ensurePlayer func(http.Handler) http.Handler,
) {
	mux.Handle("POST /api/sessions", ensurePlayer(clientapi.HandleSessionCreate(logger, sessionService)))
	mux.Handle("POST /api/sessions/{code}/join", ensurePlayer(clientapi.HandleSessionJoin(logger, sessionService)))
	mux.Handle("POST /api/sessions/{code}/ready", ensurePlayer(clientapi.HandleSessionReady(logger, sessionService)))
	mux.Handle("POST /api/sessions/{code}/start", ensurePlayer(clientapi.HandleSessionStart(logger, sessionService)))
	mux.Handle(
		"POST /api/sessions/{code}/arm-start",
		ensurePlayer(clientapi.HandleSessionArmStart(logger, sessionService)),
	)
	mux.Handle(
		"POST /api/sessions/{code}/cancel-start",
		ensurePlayer(clientapi.HandleSessionCancelStart(logger, sessionService)),
	)
	mux.Handle("POST /api/sessions/{code}/answer", ensurePlayer(clientapi.HandleSessionAnswer(logger, sessionService)))
	mux.Handle("POST /api/sessions/{code}/leave", ensurePlayer(clientapi.HandleSessionLeave(logger, sessionService)))
	mux.Handle("GET /api/sessions/{code}/state", ensurePlayer(clientapi.HandleSessionState(logger, sessionService)))
	mux.Handle(
		"GET /api/sessions/{code}/events",
		ensurePlayer(clientapi.HandleSessionEvents(logger, sessionService, sessionHub)),
	)
}

// addHostRoutes registers the host presentation surface (MP-3 / #680): the
// "Play live" entry that opens a session and the TV lobby it redirects to,
// plus the host start control. All three are host-gated (RequireGameHost)
// and the mutating POSTs carry CSRF protection. The lobby reads live state
// through the JSON API the page polls (SSE tick -> GET /state); the host
// handlers reuse the shared session service so the page and the API see the
// same in-memory session.
func addHostRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	stores *store.Stores,
	sessions *session.Manager,
	csrfMgr *csrf.Manager,
	sessionService *livesession.Service,
) {
	requireGameHost := func(h http.Handler) http.Handler {
		return auth.RequireGameHost(auth.RequireVerifiedEmail(h), stores.Players, sessions, csrfMgr, logger)
	}
	csrfMW := csrfMgr.Middleware

	handlers := host.NewHandlers(logger, csrfMgr, sessionService)

	mux.Handle("POST /host", csrfMW(requireGameHost(http.HandlerFunc(handlers.Create))))
	mux.Handle("GET /host/{code}", requireGameHost(http.HandlerFunc(handlers.Lobby)))
	mux.Handle("POST /host/{code}/start", csrfMW(requireGameHost(http.HandlerFunc(handlers.Start))))
}
