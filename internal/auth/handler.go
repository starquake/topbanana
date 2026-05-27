package auth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/csrf"
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
// 64 KiB is comfortable for username + password + csrf_token while denying
// an attacker the ability to exhaust memory by streaming a multi-megabyte
// body into r.ParseForm. Wraps r.Body before any form-parsing call.
const maxFormBodySize = 64 * 1024

// formData is the data passed to the register and login templates.
type formData struct {
	Title    string
	Username string
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
// redirected to the role-appropriate landing page instead — the
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
	AdminUsernames []string
	GoogleEnabled  bool
	Mailer         VerifyEmailSender
	Tokens         VerifyTokenStore
	BaseURL        string
}

// HandleRegisterSubmit handles POST /register. When the caller already
// has an anonymous session row, the handler upgrades that row via
// ClaimPlayer so the visitor's game history follows them; if the row
// was concurrently claimed it falls back to CreatePlayer. Usernames in
// adminUsernames are promoted to admin; the first password-bearing
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing register form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		rawUsername := r.PostFormValue("username")
		rawEmail := r.PostFormValue("email")
		password := r.PostFormValue("password")

		input := validateRegisterInput(rawUsername, rawEmail, password)
		if !input.OK {
			render.render(w, r, http.StatusBadRequest, formData{
				Title:      "Register",
				Username:   input.CleanedUsername,
				Email:      input.CleanedEmail,
				Message:    input.ErrMsg,
				ShowGoogle: deps.GoogleEnabled,
			})

			return
		}

		role := RolePlayer
		if slices.Contains(deps.AdminUsernames, input.CleanedUsername) {
			role = RoleAdmin
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		player, err := claimOrCreatePlayer(
			r, players, sessions, input.CleanedUsername, input.CleanedEmail, hashed, role,
		)
		if err != nil {
			if msg, ok := registerCollisionMessage(err); ok {
				render.render(w, r, http.StatusConflict, formData{
					Title:      "Register",
					Username:   input.CleanedUsername,
					Email:      input.CleanedEmail,
					Message:    msg,
					ShowGoogle: deps.GoogleEnabled,
				})

				return
			}
			logger.ErrorContext(r.Context(), "error creating player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		dispatchVerifyEmail(r.Context(), logger, deps, player.ID, input.CleanedEmail)
		sessions.Set(w, player.ID)
		http.Redirect(w, r, landingPathFor(player.Role), http.StatusSeeOther)
	})
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

// registerCollisionMessage maps the register-time conflict sentinels
// onto the user-facing banner. ok=false when err is something else
// and the caller should treat it as a 500.
func registerCollisionMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrUsernameTaken):
		return "Username is already taken.", true
	case errors.Is(err, ErrEmailTaken):
		return "Email is already registered. Try logging in.", true
	}

	return "", false
}

// claimOrCreatePlayer upgrades an anonymous session row via ClaimPlayer
// when possible, falling back to CreatePlayer if no session exists or
// the row was claimed by a concurrent registration sharing the same
// cookie.
func claimOrCreatePlayer(
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	username, email, passwordHash, requestedRole string,
) (*Player, error) {
	playerID, ok := sessions.PlayerID(r)
	if !ok {
		return createPlayerWrapped(r, players, username, email, passwordHash, requestedRole)
	}

	existing, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, username, email, passwordHash, requestedRole)
		}

		return nil, fmt.Errorf("get player by id for claim: %w", err)
	}
	if !existing.IsAnonymous() {
		// Session already belongs to a credentialled account. Treat this as a
		// fresh registration so the new account is created independently;
		// the existing session is replaced when the caller resets the cookie.
		return createPlayerWrapped(r, players, username, email, passwordHash, requestedRole)
	}

	claimed, err := players.ClaimPlayer(r.Context(), playerID, username, email, passwordHash, requestedRole)
	if err != nil {
		if errors.Is(err, ErrPlayerAlreadyClaimed) || errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, username, email, passwordHash, requestedRole)
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
	username, email, passwordHash, requestedRole string,
) (*Player, error) {
	p, err := players.CreatePlayer(r.Context(), username, email, passwordHash, requestedRole)
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

// HandleLoginSubmit returns a handler for POST /login. It verifies the
// credentials, signs the player in, and redirects to the admin landing page.
// registrationEnabled controls whether error renders show the "No account? Register" link.
// googleEnabled controls whether error renders show the "Sign in with Google" button.
// games is the post-login migration hook (#406): when the request
// arrives with a session pointing at an anonymous row, that row's
// game history is carried onto the signed-in account. Nil disables
// the migration — accepted by callers (tests) that don't care about
// the migration path.
func HandleLoginSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	games AnonymousGameMigrator,
	registrationEnabled, googleEnabled bool,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	// dummyHash computes a bcrypt hash once on first use. The handler runs
	// CheckPassword against it when the username does not exist so the
	// response time matches the valid-username path, preventing username
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

		// Trim the username so a registrant who entered "alice" can still log in
		// after typing "alice " (trailing space). The store also trims as
		// defense in depth.
		username := strings.TrimSpace(r.PostFormValue("username"))
		password := r.PostFormValue("password")

		player, err := players.GetPlayerByUsername(r.Context(), username)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				// Equalise timing with the valid-username path so an attacker
				// cannot enumerate usernames by response time.
				_ = CheckPassword(dummyHash(), password)
				renderInvalidCredentials(render, w, r, username, registrationEnabled, googleEnabled)

				return
			}
			logger.ErrorContext(r.Context(), "error looking up player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if player.PasswordHash == "" {
			// Player has no password set (e.g. legacy seed admin). Run the
			// dummy compare to keep timing consistent.
			_ = CheckPassword(dummyHash(), password)
			renderInvalidCredentials(render, w, r, username, registrationEnabled, googleEnabled)

			return
		}

		if err := CheckPassword(player.PasswordHash, password); err != nil {
			renderInvalidCredentials(render, w, r, username, registrationEnabled, googleEnabled)

			return
		}

		var priorSessionPlayerID *int64
		if id, ok := sessions.PlayerID(r); ok {
			priorSessionPlayerID = &id
		}
		sessions.Set(w, player.ID)
		migrateGamesAfterSignIn(r.Context(), logger, players, games, priorSessionPlayerID, player.ID)
		redirectAfterLogin(w, r, player.Role)
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
	// CleanedUsername is the username, whitespace-trimmed.
	CleanedUsername string
	// CleanedEmail is the email, whitespace-trimmed and lowercased so
	// case variants cannot create duplicate accounts.
	CleanedEmail string
	// ErrMsg is a user-facing error message, populated only when OK is false.
	ErrMsg string
	// OK reports whether the inputs are valid.
	OK bool
}

// validateRegisterInput trims the username and email, lowercases the
// email, and validates the inputs.
func validateRegisterInput(username, email, password string) registerInput {
	cleanedUsername := strings.TrimSpace(username)
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	if cleanedUsername == "" {
		return registerInput{
			CleanedUsername: cleanedUsername, CleanedEmail: cleanedEmail,
			ErrMsg: "Username is required.", OK: false,
		}
	}
	if !looksLikeEmail(cleanedEmail) {
		return registerInput{
			CleanedUsername: cleanedUsername, CleanedEmail: cleanedEmail,
			ErrMsg: "Enter a valid email address.", OK: false,
		}
	}
	if len(password) < MinPasswordLength {
		return registerInput{
			CleanedUsername: cleanedUsername, CleanedEmail: cleanedEmail,
			ErrMsg: fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength),
			OK:     false,
		}
	}
	if len(password) > MaxPasswordLength {
		// bcrypt rejects passwords above MaxPasswordLength; catching it here
		// turns a wrapped 500 into a normal form-validation error with a
		// user-friendly message.
		return registerInput{
			CleanedUsername: cleanedUsername, CleanedEmail: cleanedEmail,
			ErrMsg: fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLength),
			OK:     false,
		}
	}

	return registerInput{CleanedUsername: cleanedUsername, CleanedEmail: cleanedEmail, OK: true}
}

// looksLikeEmail is a deliberately loose check: one '@', non-empty
// local part, host with a dot that does not start or end with one.
// Tight validation belongs at the SMTP / DNS layer.
func looksLikeEmail(s string) bool {
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
// "Invalid username or password." banner. Preserves the submitted
// username so the visitor can fix just the password, and preserves
// the posted next so a failed attempt does not drop the visitor's
// intended destination (#449).
func renderInvalidCredentials(
	render *templateRenderer,
	w http.ResponseWriter,
	r *http.Request,
	username string,
	registrationEnabled, googleEnabled bool,
) {
	render.render(w, r, http.StatusUnauthorized, formData{
		Title:        "Log in",
		Username:     username,
		Message:      "Invalid username or password.",
		ShowRegister: registrationEnabled,
		ShowGoogle:   googleEnabled,
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
	// MinPasswordLength/MaxPasswordLength constants — drift between the
	// form, the validator, and the bcrypt cap stays impossible without
	// touching the constants directly.
	funcs := template.FuncMap{
		"csrfToken": func() string { return "" },
		"ogImage":   func() string { return "" },
		"passwordHelp": func() string {
			return fmt.Sprintf("Must be %d–%d characters.", MinPasswordLength, MaxPasswordLength)
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
