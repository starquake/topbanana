package auth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/session"
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

const (
	adminLandingPath  = "/admin/quizzes"
	playerLandingPath = "/"
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
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Registration doesn't carry next today (out of scope per #449);
		// pass empty so the helper falls back to the role landing for
		// already-signed-in visitors.
		if redirectIfSignedIn(w, r, players, sessions, "") {
			return
		}
		render.render(w, r, http.StatusOK, formData{Title: "Register", ShowGoogle: googleEnabled})
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
	AdminEmails   []string
	GoogleEnabled bool
	Mailer        VerifyEmailSender
	Tokens        VerifyTokenStore
	BaseURL       string
}

// HandleRegisterSubmit handles POST /register. When the caller already
// has an anonymous session row, the handler upgrades that row via
// ClaimPlayer so the visitor's game history follows them; if the row
// was concurrently claimed it falls back to CreatePlayer. Emails in
// deps.AdminEmails are promoted to admin; the first password-bearing
// registrant is atomically promoted by the store (see CreatePlayer).
// On success the handler dispatches a verification email best-effort
// so an SMTP outage does not block the signup.
func HandleRegisterSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	deps RegisterDeps,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")
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

		input := validateRegisterInput(rawDisplayName, rawEmail, password, passwordConfirm)
		if !input.OK {
			render.render(w, r, http.StatusBadRequest, formData{
				Title:       "Register",
				DisplayName: input.CleanedDisplayName,
				Email:       input.CleanedEmail,
				Message:     input.ErrMsg,
				ShowGoogle:  deps.GoogleEnabled,
			})

			return
		}

		role := RolePlayer
		if slices.Contains(deps.AdminEmails, input.CleanedEmail) {
			role = RoleAdmin
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		renderers := registerRenderers{form: render, pending: pending, sessions: sessions}

		player, err := claimOrCreatePlayer(
			r, players, sessions, input.CleanedDisplayName, input.CleanedEmail, hashed, role,
		)
		if err != nil {
			handleRegisterError(w, r, logger, renderers, deps, input, err)

			return
		}

		dispatchVerifyEmail(r.Context(), logger, deps, player.ID, input.CleanedEmail)
		renderers.renderPending(w, r, input.CleanedEmail)
	})
}

// registerRenderers bundles the two register-flow renderers and the
// session manager so the error/pending helpers stay under revive's
// argument-count cap.
type registerRenderers struct {
	form     *templateRenderer
	pending  *templateRenderer
	sessions *session.Manager
}

// renderPending clears any prior session and renders the confirmation
// page. The hard email-verification gate (#574) means no session is set
// until the address is proven; clearing unconditionally signs out the
// anonymous-upgrade path (ClaimPlayer) so a pre-existing anonymous cookie
// cannot leave the unverified account signed in.
func (rr registerRenderers) renderPending(w http.ResponseWriter, r *http.Request, email string) {
	rr.sessions.Clear(w)
	rr.pending.render(w, r, http.StatusOK, registerPendingData{
		Title: "Verify your email",
		Email: email,
	})
}

// handleRegisterError writes the response for a failed
// claimOrCreatePlayer. ErrEmailTaken is account-existence-opaque: a
// distinct 409 would turn the form into an enumeration oracle, so it
// responds exactly as the fresh-signup branch does and notifies the real
// owner out of band instead (#573). ErrDisplayNameTaken re-renders the form
// with a 409 - a display name is a public handle, not an enumeration
// secret. Anything else is a 500.
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
		renderers.form.render(w, r, http.StatusConflict, formData{
			Title:       "Register",
			DisplayName: input.CleanedDisplayName,
			Email:       input.CleanedEmail,
			Message:     "That display name is already taken.",
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
	recipient string,
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
	go func() {
		defer cancel()
		SendVerifyEmailBestEffort(bg, logger, deps.Tokens, deps.Mailer,
			deps.BaseURL, recipient, playerID, time.Now().UTC())
	}()
}

// dispatchRegisterExisting notifies the owner of an already-registered
// address that someone attempted to register with it. The collided
// address IS the existing owner's verified address, so sending to
// recipient reaches the real owner; the unauthenticated submitter
// learns nothing. Runs on a detached goroutine with a bounded timeout,
// mirroring dispatchVerifyEmail, so the email-collision response
// returns on the same timing path as the success response. A nil
// Mailer (unit tests) skips the send; a send failure is logged at Warn
// and never surfaced.
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
	go func() {
		defer cancel()
		msg := mailer.Message{
			To:      recipient,
			Subject: "Someone tried to register with your Top Banana! email",
			Body: "Someone tried to create a Top Banana! account with this email address.\n\n" +
				"An account already exists for this address. If it was you, sign in or reset your password instead.\n\n" +
				"If it was not you, no action is needed.\n",
			Kind: mailer.KindRegisterExisting,
		}
		if err := deps.Mailer.Send(bg, msg); err != nil {
			logger.WarnContext(bg, "register-existing notice failed", slog.Any("err", err))
		}
	}()
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
	registrationEnabled, googleEnabled bool,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := SafeNextPath(r.URL.Query().Get("next"))
		if redirectIfSignedIn(w, r, players, sessions, next) {
			return
		}
		render.render(w, r, http.StatusOK, formData{
			Title:        "Log in",
			ShowRegister: registrationEnabled,
			ShowGoogle:   googleEnabled,
			Next:         next,
		})
	})
}

// redirectIfSignedIn writes a 303 when the request already carries a
// session pointing at an authenticated player. Honours next when it is
// a [SafeNextPath]-validated value so a deep link visitor with a live
// session lands on their intended page; falls back to the role
// landing otherwise. Returns true if it wrote a response - the caller
// must skip its own render in that case. Errors during the
// session/player lookup fall through to a normal render so a
// transient DB hiccup doesn't lock the visitor out of the auth pages.
func redirectIfSignedIn(
	w http.ResponseWriter,
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	next string,
) bool {
	playerID, ok := sessions.PlayerID(r)
	if !ok {
		return false
	}
	player, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil || !player.IsAuthenticated() {
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
// branch cannot starve the user-driven resend and vice versa. The
// bundle exists so HandleLoginSubmit stays under revive's argument
// limit as the dep set grows.
type LoginDeps struct {
	Players             PlayerStore
	Sessions            *session.Manager
	Games               AnonymousGameMigrator
	Limiter             *LoginRateLimiter
	Mailer              VerifyEmailSender
	Tokens              VerifyTokenStore
	ResendLimiter       *VerifyResendLimiter
	BaseURL             string
	RegistrationEnabled bool
	GoogleEnabled       bool
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
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")
	formCfg := loginFormCfg{
		render:              render,
		registrationEnabled: deps.RegistrationEnabled,
		googleEnabled:       deps.GoogleEnabled,
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
		if wait, allowed := deps.Limiter.Allow(deps.Limiter.ClientIP(r)); !allowed {
			renderLoginRateLimited(formCfg, w, r, email, wait)

			return
		}

		player, ok := authenticateLogin(logger, formCfg, w, r, deps.Players, dummyHash, email, password)
		if !ok {
			return
		}

		// Credentials are correct but the account email is unverified
		// (#492): refuse the sign-in, re-render the login form with a
		// banner that names the address, and dispatch a fresh verify
		// link best-effort. The login limiter was already stamped above,
		// so this branch counts toward the per-IP cap just like a
		// wrong-password attempt.
		if !player.IsEmailVerified() {
			renderUnverifiedLogin(logger, formCfg, deps, w, r, player)

			return
		}

		var priorSessionPlayerID *int64
		if id, ok := deps.Sessions.PlayerID(r); ok {
			priorSessionPlayerID = &id
		}
		deps.Sessions.Set(w, player.ID, player.SessionVersion)
		migrateGamesAfterSignIn(r.Context(), logger, deps.Players, deps.Games, priorSessionPlayerID, player.ID)
		redirectAfterLogin(w, r, player.Role)
	})
}

// loginFormCfg bundles the per-handler login form render config so
// the inner helpers (authenticateLogin, renderInvalidCredentials,
// renderLoginRateLimited) stay under revive's argument-limit.
type loginFormCfg struct {
	render              *templateRenderer
	registrationEnabled bool
	googleEnabled       bool
}

// authenticateLogin runs the lookup-then-bcrypt half of the login
// flow. Returns (player, true) on a valid match; writes the response
// itself and returns (nil, false) on every miss/error path so the
// caller can early-return. Extracted from HandleLoginSubmit so that
// handler stays under revive's function-length limit once the rate
// limiter check is wired in front of it (#494).
func authenticateLogin(
	logger *slog.Logger,
	cfg loginFormCfg,
	w http.ResponseWriter,
	r *http.Request,
	players PlayerStore,
	dummyHash func() string,
	email, password string,
) (*Player, bool) {
	player, err := players.GetPlayerByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			// Equalise timing with the valid-email path so an attacker
			// cannot enumerate emails by response time.
			_ = CheckPassword(dummyHash(), password)
			renderInvalidCredentials(cfg, w, r, email)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error looking up player", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}

	if player.PasswordHash == "" {
		// Legacy seed admin with no hash on file. Run the dummy compare
		// to keep timing consistent.
		_ = CheckPassword(dummyHash(), password)
		renderInvalidCredentials(cfg, w, r, email)

		return nil, false
	}

	if err := CheckPassword(player.PasswordHash, password); err != nil {
		renderInvalidCredentials(cfg, w, r, email)

		return nil, false
	}

	return player, true
}

// renderUnverifiedLogin re-renders the login form with the
// "please verify your email" banner naming the resolved address, and
// fires a fresh verify-email send through the per-IP resend limiter.
// Status stays 200 OK so an unverified visitor who reloads sees the
// form again without the browser flagging a 4xx (the response shape
// mirrors the verify-email/request flow's success render).
//
// Telling the visitor "we resent the link to <email>" leaks that the
// credentials were correct - an enumeration oracle. Accepted as the
// UX cost: a generic "invalid email or password" here would make
// unverified-but-correct logins feel like wrong-password to a real
// user. Decided in #492.
func renderUnverifiedLogin(
	logger *slog.Logger,
	cfg loginFormCfg,
	deps LoginDeps,
	w http.ResponseWriter,
	r *http.Request,
	player *Player,
) {
	dispatchVerifyResend(r, logger, deps, player)
	cfg.render.render(w, r, http.StatusOK, formData{
		Title:        "Log in",
		Email:        player.Email,
		Message:      "Please verify your email - we just resent the link to " + player.Email + ".",
		ShowRegister: cfg.registrationEnabled,
		ShowGoogle:   cfg.googleEnabled,
		Next:         SafeNextPath(r.PostFormValue("next")),
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
	bg, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), verifyEmailDispatchTimeout)
	go func() {
		defer cancel()
		SendVerifyEmailBestEffort(bg, logger, deps.Tokens, deps.Mailer,
			deps.BaseURL, player.Email, player.ID, time.Now().UTC())
	}()
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
	cfg.render.render(w, r, http.StatusTooManyRequests, formData{
		Title:        "Log in",
		Email:        email,
		Message:      "Too many attempts. Try again in a moment.",
		ShowRegister: cfg.registrationEnabled,
		ShowGoogle:   cfg.googleEnabled,
		Next:         SafeNextPath(r.PostFormValue("next")),
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
// redirects to /login.
func HandleLogout(sessions *session.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions.Clear(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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
func validateRegisterInput(displayName, email, password, passwordConfirm string) registerInput {
	cleanedDisplayName := strings.TrimSpace(displayName)
	if cleanedDisplayName == "" {
		cleanedDisplayName = GeneratePetname()
	}
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	if !LooksLikeEmail(cleanedEmail) {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: "Enter a valid email address.", OK: false,
		}
	}
	if len(password) < MinPasswordLength {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength),
			OK:     false,
		}
	}
	if len(password) > MaxPasswordLength {
		// bcrypt rejects passwords above MaxPasswordLength; catching it here
		// turns a wrapped 500 into a normal form-validation error with a
		// user-friendly message.
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLength),
			OK:     false,
		}
	}
	if password != passwordConfirm {
		return registerInput{
			CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail,
			ErrMsg: "Passwords do not match.", OK: false,
		}
	}

	return registerInput{CleanedDisplayName: cleanedDisplayName, CleanedEmail: cleanedEmail, OK: true}
}

// LooksLikeEmail is the shared shape check used by the register flow,
// the verify-resend gate, and the in-session email-change flow (#497).
// Deliberately loose: one '@', non-empty local part, host with a dot
// that does not start or end with one. Tight validation belongs at the
// SMTP / DNS layer, not in the form handler.
func LooksLikeEmail(s string) bool {
	if s == "" {
		return false
	}
	local, host, ok := strings.Cut(s, "@")
	if !ok || local == "" || host == "" {
		return false
	}
	if strings.Count(s, "@") > 1 {
		return false
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	if !strings.Contains(host, ".") {
		return false
	}

	return true
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
	cfg.render.render(w, r, http.StatusUnauthorized, formData{
		Title:        "Log in",
		Email:        email,
		Message:      "Invalid email or password.",
		ShowRegister: cfg.registrationEnabled,
		ShowGoogle:   cfg.googleEnabled,
		// Preserve the posted next so a failed login attempt does not
		// drop the visitor's intended destination (#449).
		Next: SafeNextPath(r.PostFormValue("next")),
	})
}

// templateRenderer renders a template combined with the auth layouts.
//
// The CSRF manager wires the {{csrfToken}} template func: each render asks the
// manager for the token, which sets a nonce cookie on the response when needed
// and returns the HMAC-derived token for the form's hidden field. The
// placeholder registered in newTemplateRenderer keeps templates parseable
// without a manager (e.g. unit tests).
type templateRenderer struct {
	logger *slog.Logger
	csrf   *csrf.Manager
	t      *template.Template
}

func newTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, page string) *templateRenderer {
	// passwordHelp keeps the form's static help text bound to the
	// MinPasswordLength/MaxPasswordLength constants - drift between the
	// form, the validator, and the bcrypt cap stays impossible without
	// touching the constants directly.
	funcs := template.FuncMap{
		"csrfToken":   func() string { return "" },
		"ogImage":     func() string { return "" },
		"envTitleTag": envtag.Get,
		"passwordHelp": func() string {
			return fmt.Sprintf("Must be %d-%d characters.", MinPasswordLength, MaxPasswordLength)
		},
	}
	layouts := template.Must(
		template.New("").Funcs(funcs).ParseFS(tmpl.FS, "auth/layouts/*.gohtml"),
	)

	return &templateRenderer{
		logger: logger,
		csrf:   csrfMgr,
		t:      template.Must(template.Must(layouts.Clone()).ParseFS(tmpl.FS, page)),
	}
}

func (tr *templateRenderer) render(w http.ResponseWriter, r *http.Request, status int, data any) {
	t, err := tr.t.Clone()
	if err != nil {
		tr.logger.ErrorContext(r.Context(), "error cloning template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	csrfToken := ""
	if tr.csrf != nil {
		csrfToken = tr.csrf.Token(w, r)
	}

	t = t.Funcs(template.FuncMap{
		"csrfToken": func() string { return csrfToken },
		"ogImage":   func() string { return absurl.BaseURL(r) + "/assets/og-image.png" },
	})

	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}
