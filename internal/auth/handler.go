package auth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/version"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// MinPasswordLength is the minimum number of bytes required for a password.
const MinPasswordLength = 13

// MaxPasswordLength is bcrypt's hard limit, in bytes: current
// [golang.org/x/crypto/bcrypt] returns [bcrypt.ErrPasswordTooLong] above this,
// and older versions silently truncated. We surface the cap up front in both
// the public registration handler and the operator -reset-password tool so
// callers receive a typed/friendly error instead of a wrapped bcrypt failure.
//
// The user-facing register-form message phrases the limit in "characters"
// because typical input is ASCII (1 byte == 1 char) and "bytes" is jargon
// for end users; the operator-tool message says "bytes" because the audience
// already knows what that means. Both check the same byte length.
const MaxPasswordLength = 72

// MaxDisplayNameLength caps a display name in runes. It is rendered to every
// other player on leaderboards and rosters, so the length is bounded as both a
// layout and an abuse guard. Every display-name entry point enforces this.
const MaxDisplayNameLength = 50

const (
	adminLandingPath  = "/admin/quizzes"
	playerLandingPath = "/"
	// loginPendingApprovalPath is the GET page every sign-in path redirects an
	// unapproved account to under LOGIN_APPROVAL_REQUIRED (#1227).
	loginPendingApprovalPath = "/login/pending-approval"
)

// Structured-log attribute keys for the authentication-outcome lines
// (#872). Each login attempt emits one line keyed on these so a host
// reviewing the server log can tell why a player could not sign in.
// Email appears here on purpose: this is the operator's private server
// log, and it is what lets a host tell which player is stuck. The HTTP
// response stays generic, so these keys must never be echoed back to a
// caller (no enumeration regression).
const (
	logEmailKey  = "email"
	logPlayerKey = "player"
	logRoleKey   = "role"
	logIPKey     = "ip"
	logReasonKey = "reason"
	logWaitKey   = "wait"
)

// Failure-reason values for the "login failed: invalid credentials"
// line. Server-log only (see the attribute-key comment above): the HTTP
// response is the same generic banner for all three, so surfacing this
// distinction to the caller would reintroduce account enumeration.
const (
	reasonUnknownAccount = "unknown-account"
	reasonNoPassword     = "no-password"
	reasonWrongPassword  = "wrong-password"
)

// landingPathFor returns the post-auth redirect target for the given
// role. Admins land on the quiz dashboard; everyone else lands on the
// public home page, which is the only place a non-admin player has
// reason to be after signing in (#288). Sending players to
// adminLandingPath used to bounce them to the "Access denied" page,
// which is honest but useless.
func landingPathFor(role string) string {
	if role == RoleAdmin {
		return adminLandingPath
	}

	return playerLandingPath
}

// maxFormBodySize caps the request body for login/register form posts.
// 64 KiB is comfortable for email + password + csrf_token while denying
// an attacker the ability to exhaust memory by streaming a multi-megabyte
// body into r.ParseForm. Wraps r.Body before any form-parsing call.
const maxFormBodySize = 64 * 1024

// formData is the data passed to the register and login templates.
type formData struct {
	Title       string
	DisplayName string
	// Email is the trimmed+lowercased value; preserved across form
	// re-renders so a failed validation doesn't drop it.
	Email   string
	Message string
	// ShowRegister controls whether the login template renders the
	// "No account? Register" link. False when REGISTRATION_ENABLED is unset/false.
	ShowRegister bool
	// ShowGoogle controls whether the login template renders the
	// "Sign in with Google" button. False when any of the
	// GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET / GOOGLE_REDIRECT_URL
	// env vars is unset.
	ShowGoogle bool
	// ShowForgotPassword renders the "Forgot your password?" link; false
	// when SMTP is unconfigured and the reset routes are unmounted (#1170).
	ShowForgotPassword bool
	// Next is the validated same-site return path the login flow
	// should send the user to on success (#449). Empty string means
	// "no return target known"; callers fall back to the role landing.
	Next string
}

// HandleRegisterForm returns a handler for GET /register that renders the
// registration form. googleEnabled controls whether the template shows
// the "Sign up with Google" button. An already-authenticated visitor is
// redirected to the role-appropriate landing page instead - the
// register form is a no-op for someone who already has an account.
func HandleRegisterForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	googleEnabled bool,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Registration doesn't carry next today (out of scope per #449);
		// pass empty so the helper falls back to the role landing for
		// already-signed-in visitors.
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}
		renderer.Render(w, r, http.StatusOK, formData{Title: "Register", ShowGoogle: googleEnabled})
	})
}

// RegisterDeps is the bundle of optional dependencies the register
// handler needs to dispatch the email verification link. Bundled into
// a struct so the handler signature stays under revive's
// argument-limit cap as the dep set grows (#111). Mailer / Tokens /
// BaseURL together cover the verify-email side; leave them as their
// zero values and SendVerifyEmailBestEffort logs a warning instead of
// sending, which is the right behaviour for unit tests.
type RegisterDeps struct {
	GoogleEnabled bool
	Mailer        VerifyEmailSender
	Tokens        VerifyTokenStore
	BaseURL       string
	// Tasks tracks the detached verify-email dispatch so a graceful
	// shutdown drains it before the DB closes (#740). Nil in unit tests,
	// which then run the dispatch untracked.
	Tasks *bgtasks.Tracker
}

// HandleRegisterSubmit handles POST /register. When the caller already
// has an anonymous session row, the handler upgrades that row via
// ClaimPlayer so the visitor's game history follows them; if the row
// was concurrently claimed it falls back to CreatePlayer. Registrants
// are always created as plain players: the ADMIN_EMAILS allowlist is
// consulted at email-verify time (#785), not here, so admin is never
// stamped on an unproven address. The first password-bearing registrant
// is still atomically promoted by the store (see CreatePlayer). On
// success the handler dispatches a verification email best-effort so an
// SMTP outage does not block the signup.
func HandleRegisterSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	deps RegisterDeps,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")
	pending := newTemplateRenderer(logger, csrfMgr, "auth/pages/register_pending.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing register form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		rawDisplayName := r.PostFormValue("display_name")
		rawEmail := r.PostFormValue("email")
		password := r.PostFormValue("password")
		passwordConfirm := r.PostFormValue("password_confirm")

		input := validateRegisterInput(locale.Resolve(r), rawDisplayName, rawEmail, password, passwordConfirm)
		if !input.OK {
			renderer.Render(w, r, http.StatusBadRequest, formData{
				Title:       "Register",
				DisplayName: input.CleanedDisplayName,
				Email:       input.CleanedEmail,
				Message:     input.ErrMsg,
				ShowGoogle:  deps.GoogleEnabled,
			})

			return
		}

		// Do NOT promote to admin based on the submitted email here: the
		// address is unproven at registration. The ADMIN_EMAILS allowlist
		// is consulted at email-verify time instead (#785), once the
		// address is proven. The store's own "first password-bearing
		// registrant becomes admin" rule (CreatePlayer/ClaimPlayer) is a
		// separate bootstrap concern and stays.
		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		renderers := registerRenderers{form: renderer, pending: pending, sessions: sessions}

		player, err := claimOrCreatePlayer(
			r, players, sessions, input.CleanedDisplayName, input.CleanedEmail, hashed, RolePlayer,
		)
		if err != nil {
			handleRegisterError(w, r, logger, renderers, deps, input, err)

			return
		}

		dispatchVerifyEmail(r.Context(), logger, deps, player.ID, input.CleanedEmail, locale.Resolve(r))
		renderers.renderPending(w, r, input.CleanedEmail)
	})
}

// registerRenderers bundles the two register-flow renderers and the
// session manager so the error/pending helpers stay under revive's
// argument-count cap.
type registerRenderers struct {
	form     *render.Renderer
	pending  *render.Renderer
	sessions *session.Manager
}

// renderPending clears any prior session and renders the confirmation
// page. The hard email-verification gate (#574) means no session is set
// until the address is proven; clearing unconditionally signs out the
// anonymous-upgrade path (ClaimPlayer) so a pre-existing anonymous cookie
// cannot leave the unverified account signed in.
func (rr registerRenderers) renderPending(w http.ResponseWriter, r *http.Request, email string) {
	rr.sessions.Clear(w)
	rr.pending.Render(w, r, http.StatusOK, registerPendingData{
		Title: locale.Translate(locale.Resolve(r), "verifyEmailPending.heading"),
		Email: email,
	})
}

// handleRegisterError writes the response for a failed claimOrCreatePlayer.
// ErrEmailTaken is account-existence-opaque (#573): a distinct 409 would make
// the form an enumeration oracle, so it responds like the fresh-signup branch
// and notifies the real owner out of band. ErrDisplayNameTaken gets a 409 (a
// display name is public, not a secret); anything else is a 500.
func handleRegisterError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	renderers registerRenderers,
	deps RegisterDeps,
	input registerInput,
	err error,
) {
	switch {
	case errors.Is(err, ErrEmailTaken):
		dispatchRegisterExisting(r.Context(), logger, deps, input.CleanedEmail)
		renderers.renderPending(w, r, input.CleanedEmail)
	case errors.Is(err, ErrDisplayNameTaken):
		renderers.form.Render(w, r, http.StatusConflict, formData{
			Title:       "Register",
			DisplayName: input.CleanedDisplayName,
			Email:       input.CleanedEmail,
			Message:     locale.Translate(locale.Resolve(r), "register.displayNameTaken"),
			ShowGoogle:  deps.GoogleEnabled,
		})
	default:
		logger.ErrorContext(r.Context(), "error creating player", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// registerPendingData backs the post-register confirmation page shown
// after a successful signup. The visitor has no session at this point
// (the hard gate clears it), so the template is the no-session resend
// variant pointing at /verify-email/request.
type registerPendingData struct {
	Title string
	Email string
}

// verifyEmailDispatchTimeout caps the detached SMTP attempt spawned by
// dispatchVerifyEmail. Set above mailer.SendTimeout so the inner dial
// gets its own 30 s budget before this outer timeout fires; the extra
// margin covers GenerateVerifyToken + CreateVerifyToken.
const verifyEmailDispatchTimeout = 45 * time.Second

// dispatchVerifyEmail fires the verify-email send on a background
// goroutine so the register response is not held open while SMTP dials.
// The spawned context is detached from r.Context() so a closed browser
// tab does not cancel the send. The deps fields are nil/empty in unit
// tests that do not exercise the mail path; a partial-but-nonzero deps
// (Mailer/Tokens set, BaseURL missing) is treated as a misconfiguration
// and warned about so an operator notices.
func dispatchVerifyEmail(
	ctx context.Context,
	logger *slog.Logger,
	deps RegisterDeps,
	playerID int64,
	recipient, loc string,
) {
	if deps.Mailer == nil || deps.Tokens == nil {
		return
	}
	if deps.BaseURL == "" {
		logger.WarnContext(ctx, "verify email dispatch skipped: BASE_URL is empty",
			slog.Int64("player_id", playerID))

		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyEmailDispatchTimeout)
	deps.Tasks.Go(func() {
		defer cancel()
		SendVerifyEmailBestEffort(bg, logger, deps.Tokens, deps.Mailer,
			deps.BaseURL, recipient, loc, playerID, time.Now().UTC())
	})
}

// Register-existing notice catalog keys.
const (
	emailRegisterExistingSubjectKey locale.MessageID = "email.registerExisting.subject"
	emailRegisterExistingBodyKey    locale.MessageID = "email.registerExisting.body"
)

// dispatchRegisterExisting notifies the owner of an already-registered address
// that someone tried to register with it. The collided address IS the owner's
// verified address, so sending to recipient reaches them; the submitter learns
// nothing. Runs detached with a bounded timeout, mirroring dispatchVerifyEmail,
// so the collision response keeps the same timing as the success path. A nil
// Mailer (unit tests) skips the send; failures are logged, never surfaced.
//
// Uses English, not the submitter's request locale: the submitter is
// unauthenticated and does not own the recipient mailbox.
func dispatchRegisterExisting(
	ctx context.Context,
	logger *slog.Logger,
	deps RegisterDeps,
	recipient string,
) {
	if deps.Mailer == nil {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyEmailDispatchTimeout)
	deps.Tasks.Go(func() {
		defer cancel()
		msg := mailer.Message{
			To:      recipient,
			Subject: locale.Translate(locale.LocaleEN, emailRegisterExistingSubjectKey),
			Body:    locale.Translate(locale.LocaleEN, emailRegisterExistingBodyKey),
			Kind:    mailer.KindRegisterExisting,
		}
		if err := deps.Mailer.Send(bg, msg); err != nil {
			logger.WarnContext(bg, "register-existing notice failed", slog.Any("err", err))
		}
	})
}

// claimOrCreatePlayer upgrades an anonymous session row via ClaimPlayer
// when possible, falling back to CreatePlayer if no session exists or
// the row was claimed by a concurrent registration sharing the same
// cookie.
func claimOrCreatePlayer(
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	displayName, email, passwordHash, requestedRole string,
) (*Player, error) {
	playerID, ok := sessions.PlayerID(r)
	if !ok {
		return createPlayerWrapped(r, players, displayName, email, passwordHash, requestedRole)
	}

	existing, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, displayName, email, passwordHash, requestedRole)
		}

		return nil, fmt.Errorf("get player by id for claim: %w", err)
	}
	if !existing.IsAnonymous() {
		// Session already belongs to a credentialled account. Treat this as a
		// fresh registration so the new account is created independently;
		// the existing session is replaced when the caller resets the cookie.
		return createPlayerWrapped(r, players, displayName, email, passwordHash, requestedRole)
	}

	claimed, err := players.ClaimPlayer(r.Context(), playerID, displayName, email, passwordHash, requestedRole)
	if err != nil {
		if errors.Is(err, ErrPlayerAlreadyClaimed) || errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, displayName, email, passwordHash, requestedRole)
		}

		return nil, fmt.Errorf("claim player: %w", err)
	}

	return claimed, nil
}

// createPlayerWrapped wraps PlayerStore.CreatePlayer's error so wrapcheck
// is happy while preserving sentinel errors for [errors.Is].
func createPlayerWrapped(
	r *http.Request,
	players PlayerStore,
	displayName, email, passwordHash, requestedRole string,
) (*Player, error) {
	p, err := players.CreatePlayer(r.Context(), displayName, email, passwordHash, requestedRole)
	if err != nil {
		return nil, fmt.Errorf("create player: %w", err)
	}

	return p, nil
}

// HandleLoginForm returns a handler for GET /login that renders the login form.
// registrationEnabled controls whether the template shows the "No account? Register" link.
// googleEnabled controls whether the "Sign in with Google" button is rendered.
// An already-authenticated visitor is redirected to the role-appropriate
// landing page so they don't see the form for an account they've already
// signed into.
func HandleLoginForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	registrationEnabled, googleEnabled, forgotPasswordEnabled bool,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := SafeNextPath(r.URL.Query().Get("next"))
		if redirectIfSignedIn(w, r, players, sessions, next) {
			return
		}
		renderer.Render(w, r, http.StatusOK, formData{
			Title:              "Log in",
			ShowRegister:       registrationEnabled,
			ShowGoogle:         googleEnabled,
			ShowForgotPassword: forgotPasswordEnabled,
			Next:               next,
		})
	})
}

// redirectIfSignedIn writes a 303 when the request already carries a
// session pointing at an authenticated player. Honours next when it is
// a [SafeNextPath]-validated value so a deep link visitor with a live
// session lands on their intended page; falls back to the role
// landing otherwise. Returns true if it wrote a response - the caller
// must skip its own render in that case.
//
// Resolves the session through AuthenticatedSessionPlayer (not a bare
// GetPlayerByID) so the "signed in" test here matches the one the
// gating middleware applies, session_version included. Without that
// agreement a cookie left version-stale by a password reset reads as
// signed-in here but signed-out at the gate, so /login 303s the
// visitor onto a page that 303s them straight back - an unbreakable
// loop the visitor cannot even log out of (#615). A missing/dead/stale
// cookie or an anonymous session falls through to a normal render.
func redirectIfSignedIn(
	w http.ResponseWriter,
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	next string,
) bool {
	player, ok := AuthenticatedSessionPlayer(r, players, sessions)
	if !ok {
		return false
	}
	target := next
	if target == "" {
		target = landingPathFor(player.Role)
	}
	//nolint:gosec // G710: target is either landingPathFor (constant) or a SafeNextPath-validated relative path; the validator at the call site rejects anything that could be an open redirect.
	http.Redirect(w, r, target, http.StatusSeeOther)

	return true
}

// LoginDeps bundles the dependencies HandleLoginSubmit needs to
// resend a verification email when a credentialled but unverified
// player attempts to log in (#492). Mailer / Tokens / BaseURL together
// cover the verify-email side; ResendLimiter is the per-IP cooldown
// shared with the public resend form, so a hot bucket on the login
// branch cannot starve the user-driven resend and vice versa. Limiter
// is the per-IP gap; AccountLimiter is the per-account backoff (#786),
// folded into the invalid-credentials path so a cooled-down account
// stays indistinguishable from a wrong password. The bundle exists so
// HandleLoginSubmit stays under revive's argument limit as the dep set
// grows.
type LoginDeps struct {
	Players             PlayerStore
	Sessions            *session.Manager
	Games               AnonymousGameMigrator
	Limiter             *LoginRateLimiter
	AccountLimiter      *AccountLoginLimiter
	Mailer              VerifyEmailSender
	Tokens              VerifyTokenStore
	ResendLimiter       *VerifyResendLimiter
	BaseURL             string
	RegistrationEnabled bool
	GoogleEnabled       bool
	// ForgotPasswordEnabled shows the "Forgot your password?" link on the
	// login form's error re-renders; false when SMTP is unconfigured (#1170).
	ForgotPasswordEnabled bool
	// LoginApprovalRequired holds a confirmed-but-unapproved account back at
	// sign-in until an admin approves it (#1227). Off by default; admins are
	// always approved so this never blocks an operator.
	LoginApprovalRequired bool
	// Tasks tracks the detached verify-email resend so a graceful
	// shutdown drains it before the DB closes (#740). Nil in unit tests,
	// which then run the dispatch untracked.
	Tasks *bgtasks.Tracker
}

// HandleLoginSubmit returns a handler for POST /login. It verifies the
// credentials, signs the player in, and redirects to the admin landing page.
// deps.RegistrationEnabled controls whether error renders show the "No account? Register" link.
// deps.GoogleEnabled controls whether error renders show the "Sign in with Google" button.
// deps.Games is the post-login migration hook (#406): when the request
// arrives with a session pointing at an anonymous row, that row's
// game history is carried onto the signed-in account. Nil disables
// the migration - accepted by callers (tests) that don't care about
// the migration path.
//
// When credentials check out but the player's email is unverified
// (#492), the handler re-renders the login form with a banner naming
// the address and dispatches a fresh verification email best-effort
// via deps.Mailer / deps.Tokens. The session is NOT set, so the
// visitor stays signed-out; deps.Mailer / deps.Tokens may be nil in
// unit tests, in which case the dispatch is skipped silently.
func HandleLoginSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	deps LoginDeps,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")
	formCfg := loginFormCfg{
		render:                renderer,
		registrationEnabled:   deps.RegistrationEnabled,
		googleEnabled:         deps.GoogleEnabled,
		forgotPasswordEnabled: deps.ForgotPasswordEnabled,
	}
	// dummyHash computes a bcrypt hash once on first use. The handler runs
	// CheckPassword against it when the email does not exist so the
	// response time matches the valid-email path, preventing email
	// enumeration by response timing.
	dummyHash := sync.OnceValue(func() string {
		h, err := HashPassword("timing-oracle-dummy-do-not-use")
		if err != nil {
			panic("HashPassword on constant input failed")
		}

		return h
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing login form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		// Trim + lowercase the email so a registrant who entered
		// "Alice@example.test" can still log in after typing
		// "alice@example.test " (trailing space, mixed case). The
		// store also normalises as defense in depth.
		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		password := r.PostFormValue("password")

		// Check the limiter BEFORE the credential lookup so a hot bucket
		// blocks the bcrypt compare too, and so the limiter fires whether
		// or not the submitted email exists - same shape the dummy-hash
		// timing equalisation already gives the credential-check path.
		clientIP := deps.Limiter.ClientIP(r)
		if wait, allowed := deps.Limiter.Allow(clientIP); !allowed {
			logger.WarnContext(r.Context(), "login blocked: rate limited",
				slog.String(logIPKey, clientIP),
				slog.Duration(logWaitKey, wait))
			renderLoginRateLimited(formCfg, w, r, email, wait)

			return
		}

		creds := loginCreds{
			email:          email,
			password:       password,
			dummyHash:      dummyHash,
			accountLimiter: deps.AccountLimiter,
		}
		player, ok := authenticateLogin(logger, formCfg, w, r, deps.Players, creds)
		if !ok {
			return
		}

		completeLogin(logger, formCfg, deps, w, r, player, email)
	})
}

// completeLogin finishes a credential-valid login: it applies the
// per-account cooldown gate (#786), refuses an unverified account (#492),
// or sets the session and redirects. Split out of HandleLoginSubmit so
// that constructor stays under revive's function-length limit.
func completeLogin(
	logger *slog.Logger,
	formCfg loginFormCfg,
	deps LoginDeps,
	w http.ResponseWriter,
	r *http.Request,
	player *Player,
	email string,
) {
	// Per-account backoff (#786): once an account has crossed the
	// failure threshold, refuse even a correct password until the
	// cooldown elapses, and render the SAME generic invalid-credentials
	// response a wrong password gets. This denies a brute-force the
	// chance to land a correct guess mid-spray without ever signalling
	// that the account is throttled or that it exists.
	if registerAccountFailureIfCooledDown(deps.AccountLimiter, email) {
		logger.WarnContext(r.Context(), "login blocked: account in cooldown",
			slog.String(logEmailKey, email))
		renderInvalidCredentials(formCfg, w, r, email)

		return
	}

	// Credentials are correct and the account is not cooled down, so
	// clear its failure streak (a user who finally typed the right
	// password is not penalised for earlier typos).
	clearAccountFailures(deps.AccountLimiter, email)

	// Refuse an unverified account; renderUnverifiedLogin keeps the
	// response indistinguishable from a wrong password (#492/#787/#1171).
	if !player.IsEmailVerified() {
		logger.InfoContext(r.Context(), "login blocked: email not verified",
			slog.Int64(logPlayerKey, player.ID),
			slog.String(logEmailKey, email))
		renderUnverifiedLogin(logger, formCfg, deps, w, r, player, email)

		return
	}

	// Hold a confirmed-but-unapproved account until an admin approves it (#1227).
	// Reachable only after correct credentials, so unlike the unverified gate this
	// sends the visitor to a clear informative page, not the generic
	// invalid-credentials one. No session is set.
	if deps.LoginApprovalRequired && !player.IsApproved() {
		logger.InfoContext(r.Context(), "login blocked: account not approved",
			slog.Int64(logPlayerKey, player.ID),
			slog.String(logEmailKey, email))
		http.Redirect(w, r, loginPendingApprovalPath, http.StatusSeeOther)

		return
	}

	var priorSessionPlayerID *int64
	if id, ok := deps.Sessions.PlayerID(r); ok {
		priorSessionPlayerID = &id
	}
	deps.Sessions.Set(w, player.ID, player.SessionVersion)
	logger.InfoContext(r.Context(), "login succeeded",
		slog.Int64(logPlayerKey, player.ID),
		slog.String(logEmailKey, player.Email),
		slog.String(logRoleKey, player.Role))
	migrateGamesAfterSignIn(r.Context(), logger, deps.Players, deps.Games, priorSessionPlayerID, player.ID)
	redirectAfterLogin(w, r, player.Role)
}

// registerAccountFailureIfCooledDown reports whether account is in the
// per-account cooldown (#786) and, if so, records the current attempt as
// another failure so a brute-force that keeps guessing keeps the window
// alive. The limiter caps how far the window can be pushed out, so a
// third-party spray cannot deny the owner indefinitely (#995). Nil limiter
// (unit tests that don't wire it) is never in cooldown.
func registerAccountFailureIfCooledDown(limiter *AccountLoginLimiter, account string) bool {
	if limiter == nil || !limiter.InCooldown(account) {
		return false
	}
	limiter.RegisterFailure(account)

	return true
}

// clearAccountFailures resets account's failure streak after a genuine
// credential match. Nil limiter is a no-op.
func clearAccountFailures(limiter *AccountLoginLimiter, account string) {
	if limiter == nil {
		return
	}
	limiter.RegisterSuccess(account)
}

// recordAccountFailure records one failed login for account (#786). Nil
// limiter is a no-op.
func recordAccountFailure(limiter *AccountLoginLimiter, account string) {
	if limiter == nil {
		return
	}
	limiter.RegisterFailure(account)
}

// loginFormCfg bundles the per-handler login form render config so
// the inner helpers (authenticateLogin, renderInvalidCredentials,
// renderLoginRateLimited) stay under revive's argument-limit.
type loginFormCfg struct {
	render                *render.Renderer
	registrationEnabled   bool
	googleEnabled         bool
	forgotPasswordEnabled bool
}

// loginCreds bundles the per-attempt credential inputs so
// authenticateLogin stays under revive's argument-limit once the
// dummy-hash and per-account limiter are threaded through it.
type loginCreds struct {
	email          string
	password       string
	dummyHash      func() string
	accountLimiter *AccountLoginLimiter
}

// authenticateLogin runs the lookup-then-bcrypt half of the login
// flow. Returns (player, true) on a valid match; writes the response
// itself and returns (nil, false) on every miss/error path so the
// caller can early-return. Extracted from HandleLoginSubmit so that
// handler stays under revive's function-length limit once the rate
// limiter check is wired in front of it (#494).
//
// Each credential-mismatch branch records a per-account failure (#786)
// so a focused brute-force trips the account cooldown; the internal-
// error branch does not, since a DB hiccup is not the user's fault.
func authenticateLogin(
	logger *slog.Logger,
	cfg loginFormCfg,
	w http.ResponseWriter,
	r *http.Request,
	players PlayerStore,
	creds loginCreds,
) (*Player, bool) {
	player, err := players.GetPlayerByEmail(r.Context(), creds.email)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			// Equalise timing with the valid-email path so an attacker
			// cannot enumerate emails by response time.
			_ = CheckPassword(creds.dummyHash(), creds.password)
			recordAccountFailure(creds.accountLimiter, creds.email)
			logger.InfoContext(r.Context(), "login failed: invalid credentials",
				slog.String(logEmailKey, creds.email),
				slog.String(logReasonKey, reasonUnknownAccount))
			renderInvalidCredentials(cfg, w, r, creds.email)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error looking up player", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	if player.PasswordHash == "" {
		// Legacy seed admin with no hash on file. Run the dummy compare
		// to keep timing consistent.
		_ = CheckPassword(creds.dummyHash(), creds.password)
		recordAccountFailure(creds.accountLimiter, creds.email)
		logger.InfoContext(r.Context(), "login failed: invalid credentials",
			slog.String(logEmailKey, creds.email),
			slog.String(logReasonKey, reasonNoPassword))
		renderInvalidCredentials(cfg, w, r, creds.email)

		return nil, false
	}

	if err := CheckPassword(player.PasswordHash, creds.password); err != nil {
		recordAccountFailure(creds.accountLimiter, creds.email)
		logger.InfoContext(r.Context(), "login failed: invalid credentials",
			slog.String(logEmailKey, creds.email),
			slog.String(logReasonKey, reasonWrongPassword))
		renderInvalidCredentials(cfg, w, r, creds.email)

		return nil, false
	}

	return player, true
}

// renderUnverifiedLogin refuses a correct-password login against an
// unverified account. It renders byte-identically to the wrong-password
// 401 so a correct password isn't a password oracle; the verify-email
// resend is dispatched silently (#492/#787/#1171).
func renderUnverifiedLogin(
	logger *slog.Logger,
	cfg loginFormCfg,
	deps LoginDeps,
	w http.ResponseWriter,
	r *http.Request,
	player *Player,
	email string,
) {
	dispatchVerifyResend(r, logger, deps, player)
	renderInvalidCredentials(cfg, w, r, email)
}

// loginPendingApprovalData backs the login_pending_approval.gohtml page.
type loginPendingApprovalData struct {
	Title string
}

// HandleLoginPendingApproval renders GET /login/pending-approval: the shared
// "awaiting admin approval" page every sign-in path redirects an unapproved
// account to under LOGIN_APPROVAL_REQUIRED (#1227). A distinct informative page
// (not the generic invalid-credentials response) reachable only after a
// successful auth on some path.
func HandleLoginPendingApproval(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/login_pending_approval.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderer.Render(w, r, http.StatusOK, loginPendingApprovalData{
			Title: locale.Translate(locale.Resolve(r), "loginPendingApproval.heading"),
		})
	})
}

// dispatchVerifyResend mirrors dispatchVerifyEmail but routes through
// deps.ResendLimiter so a stampede on the login form cannot burn an
// unbounded number of SMTP attempts for the same address. A blocked
// limiter is treated as success-from-the-visitor's-perspective: the
// banner still claims a resend so the response shape stays uniform
// (the previous send is, by definition, recent). Detached context +
// bounded timeout match dispatchVerifyEmail's pattern.
func dispatchVerifyResend(
	r *http.Request,
	logger *slog.Logger,
	deps LoginDeps,
	player *Player,
) {
	if deps.Mailer == nil || deps.Tokens == nil || deps.ResendLimiter == nil {
		return
	}
	if deps.BaseURL == "" {
		logger.WarnContext(r.Context(), "verify email resend skipped: BASE_URL is empty",
			slog.Int64("player_id", player.ID))

		return
	}
	if _, allowed := deps.ResendLimiter.Allow(deps.ResendLimiter.ClientIP(r)); !allowed {
		return
	}
	loc := locale.Resolve(r)
	bg, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), verifyEmailDispatchTimeout)
	deps.Tasks.Go(func() {
		defer cancel()
		SendVerifyEmailBestEffort(bg, logger, deps.Tokens, deps.Mailer,
			deps.BaseURL, player.Email, loc, player.ID, time.Now().UTC())
	})
}

// renderLoginRateLimited re-renders the login form with the
// rate-limit banner and a Retry-After header, status 429. The banner
// is account-existence-opaque: it doesn't say "wrong password" or
// "no such user", so a probe cannot tell from the rate-limited
// response whether the submitted email exists.
func renderLoginRateLimited(
	cfg loginFormCfg,
	w http.ResponseWriter,
	r *http.Request,
	email string,
	wait time.Duration,
) {
	// Round sub-second remainders up so a 0.4s wait still reports as
	// 1s; Retry-After: 0 would let a scripted client retry-loop the
	// limiter.
	seconds := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	cfg.render.Render(w, r, http.StatusTooManyRequests, formData{
		Title:              "Log in",
		Email:              email,
		Message:            locale.Translate(locale.Resolve(r), "login.rateLimited"),
		ShowRegister:       cfg.registrationEnabled,
		ShowGoogle:         cfg.googleEnabled,
		ShowForgotPassword: cfg.forgotPasswordEnabled,
		Next:               SafeNextPath(r.PostFormValue("next")),
	})
}

// redirectAfterLogin sends the user to the posted `next` path when it
// passes [SafeNextPath], otherwise to the role-appropriate landing.
// Pulled out of HandleLoginSubmit so the handler stays under revive's
// function-length limit (#449).
func redirectAfterLogin(w http.ResponseWriter, r *http.Request, role string) {
	target := SafeNextPath(r.PostFormValue("next"))
	if target == "" {
		target = landingPathFor(role)
	}
	//nolint:gosec // G710: target is either landingPathFor (constant) or a SafeNextPath-validated relative path that rejects open-redirect shapes.
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// HandleLogout returns a handler for POST /logout. It clears the session cookie and
// redirects to the home page.
func HandleLogout(sessions *session.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions.Clear(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// registerInput is the result of validateRegisterInput.
type registerInput struct {
	// CleanedDisplayName is the displayName, whitespace-trimmed. Falls back to
	// a [GeneratePetname] result when the input trims to "" so the post-
	// #446 register flow accepts a blank display name and still produces
	// a non-empty displayName for the schema's NOT NULL guard.
	CleanedDisplayName string
	// CleanedEmail is the email, whitespace-trimmed and lowercased so
	// case variants cannot create duplicate accounts.
	CleanedEmail string
	// ErrMsg is a user-facing error message, populated only when OK is false.
	ErrMsg string
	// OK reports whether the inputs are valid.
	OK bool
}

// validateRegisterInput trims the displayName and email, lowercases the
// email, and validates the inputs. A blank displayName falls back to
// [GeneratePetname] so register-with-just-email works after #446 made
// the display name optional; email is the credential identifier.
func validateRegisterInput(loc, displayName, email, password, passwordConfirm string) registerInput {
	cleanedDisplayName := strings.TrimSpace(displayName)
	if cleanedDisplayName == "" {
		cleanedDisplayName = GeneratePetname()
	}
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	if !LooksLikeEmail(cleanedEmail) {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: locale.Translate(loc, "validation.invalidEmail"), OK: false,
		}
	}
	if len(password) < MinPasswordLength {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: locale.TranslateCount(loc, "validation.passwordTooShort", MinPasswordLength),
			OK:     false,
		}
	}
	if len(password) > MaxPasswordLength {
		// bcrypt rejects passwords above MaxPasswordLength; catching it here
		// turns a wrapped 500 into a normal form-validation error with a
		// user-friendly message.
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: locale.TranslateCount(loc, "validation.passwordTooLong", MaxPasswordLength),
			OK:     false,
		}
	}
	if password != passwordConfirm {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: locale.Translate(loc, "validation.passwordsNoMatch"), OK: false,
		}
	}

	return registerInput{CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail, OK: true}
}

// renderInvalidCredentials re-renders the login form with the generic
// "Invalid email or password." banner. Preserves the submitted
// email so the visitor can fix just the password, and preserves
// the posted next so a failed attempt does not drop the visitor's
// intended destination (#449).
func renderInvalidCredentials(
	cfg loginFormCfg,
	w http.ResponseWriter,
	r *http.Request,
	email string,
) {
	cfg.render.Render(w, r, http.StatusUnauthorized, formData{
		Title:              "Log in",
		Email:              email,
		Message:            locale.Translate(locale.Resolve(r), "login.invalidCredentials"),
		ShowRegister:       cfg.registrationEnabled,
		ShowGoogle:         cfg.googleEnabled,
		ShowForgotPassword: cfg.forgotPasswordEnabled,
		// Preserve the posted next so a failed login attempt does not
		// drop the visitor's intended destination (#449).
		Next: SafeNextPath(r.PostFormValue("next")),
	})
}

// parseTemplate parses the auth layouts plus the named page, registering the
// auth surface's parse-time placeholder funcs. render rebinds the per-request
// ones (csrfToken, ogImage, viewer) at execute time.
func parseTemplate(page string) *template.Template {
	// passwordHelp keeps the form's static help text bound to the
	// MinPasswordLength/MaxPasswordLength constants - drift between the
	// form, the validator, and the bcrypt cap stays impossible without
	// touching the constants directly.
	funcs := template.FuncMap{
		"csrfToken":      func() string { return "" },
		"ogImage":        func() string { return "" },
		"envTitleTag":    envtag.Get,
		"versionLabel":   version.Label,
		"viewerName":     func() string { return "" },
		"isSignedIn":     func() bool { return false },
		"isAdmin":        func() bool { return false },
		"showSectionNav": func() bool { return false },
		"navSection":     func() string { return "" },
		"logoHref":       func() string { return "/" },
		"profileHref":    func() string { return "/profile" },
		// Parse-time placeholders; render.Renderer rebinds t/tCount/lang per request.
		"t":      func(string) string { return "" },
		"tCount": func(string, int) string { return "" },
		"lang":   func() string { return locale.LocaleEN },
		"passwordHelp": func() string {
			return fmt.Sprintf("Must be %d-%d characters.", MinPasswordLength, MaxPasswordLength)
		},
		"passwordMinLength": func() int { return MinPasswordLength },
		// Placeholder so the shared components glob parses; this surface
		// does not render quiz_card, which is the only user (#889).
		"humanizeTime": func(time.Time) string { return "" },
	}

	return render.Parse(tmpl.FS, funcs, page, "components/*.gohtml", "auth/layouts/*.gohtml")
}

// newTemplateRenderer parses the named page and wraps the tree in a
// render.Renderer bound to the auth surface's per-request funcs. The csrfToken
// func is bound per render by render.Renderer (a placeholder keeps templates
// parseable without a manager, e.g. unit tests).
func newTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, page string) *render.Renderer {
	return render.New(logger, csrfMgr, parseTemplate(page), "base.gohtml", authPerRequestFuncs)
}

// authPerRequestFuncs binds the auth surface's per-request template funcs: the
// OG image URL and the viewer's display name / signed-in flag resolved from the
// request context. render.Renderer binds csrfToken itself, so it is omitted
// here.
func authPerRequestFuncs(r *http.Request) template.FuncMap {
	displayName := ""
	signedIn := false
	if p, ok := PlayerFromContext(r.Context()); ok {
		displayName = p.DisplayName
		signedIn = true
	}

	loc := locale.Resolve(r)

	return template.FuncMap{
		"ogImage":    func() string { return absurl.BaseURL(r) + "/static/og-image.png" },
		"viewerName": func() string { return displayName },
		"isSignedIn": func() bool { return signedIn },
		// passwordHelp keeps the {min}/{max} help text bound to the constants.
		"passwordHelp": func() string {
			return locale.TranslateWith(loc, "common.passwordHelp", map[string]string{
				"min": strconv.Itoa(MinPasswordLength),
				"max": strconv.Itoa(MaxPasswordLength),
			})
		},
	}
}
