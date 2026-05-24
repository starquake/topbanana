package auth

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

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
	Message  string
	// ShowRegister controls whether the login template renders the
	// "No account? Register" link. False when REGISTRATION_ENABLED is unset/false.
	ShowRegister bool
	// ShowGoogle controls whether the login template renders the
	// "Sign in with Google" button. False when any of the
	// GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET / GOOGLE_REDIRECT_URL
	// env vars is unset.
	ShowGoogle bool
}

// HandleRegisterForm returns a handler for GET /register that renders the
// registration form. googleEnabled controls whether the template shows
// the "Sign up with Google" button.
func HandleRegisterForm(logger *slog.Logger, csrfMgr *csrf.Manager, googleEnabled bool) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.render(w, r, http.StatusOK, formData{Title: "Register", ShowGoogle: googleEnabled})
	})
}

// HandleRegisterSubmit returns a handler for POST /register. It validates the
// request, creates the player, signs them in, and redirects to the admin
// landing page.
//
// Registrants whose username appears in `adminUsernames` (case-sensitive) are
// promoted to admin. Otherwise the role passed to the store is RolePlayer and
// the store atomically promotes the very first password-bearing registrant to
// admin — see CreatePlayer for the SQL that makes this concurrency-safe.
//
// Score-claiming flow: when the request already carries a valid session for
// an anonymous player (a row created by EnsurePlayer with no password_hash),
// the handler upgrades that existing row in place via ClaimPlayer instead of
// inserting a new one. This means a visitor who plays a few games without an
// account and then registers keeps their player_id, so their game history
// follows them. If the anonymous row was concurrently claimed by another
// request the handler falls back to CreatePlayer; the visitor ends up with a
// fresh row but the registration still succeeds.
func HandleRegisterSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	adminUsernames []string,
	googleEnabled bool,
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
		password := r.PostFormValue("password")

		input := validateRegisterInput(rawUsername, password)
		if !input.OK {
			render.render(w, r, http.StatusBadRequest, formData{
				Title:      "Register",
				Username:   input.Cleaned,
				Message:    input.ErrMsg,
				ShowGoogle: googleEnabled,
			})

			return
		}

		role := RolePlayer
		if slices.Contains(adminUsernames, input.Cleaned) {
			role = RoleAdmin
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		player, err := claimOrCreatePlayer(r, players, sessions, input.Cleaned, hashed, role)
		if err != nil {
			if errors.Is(err, ErrUsernameTaken) {
				render.render(w, r, http.StatusConflict, formData{
					Title:      "Register",
					Username:   input.Cleaned,
					Message:    "Username is already taken.",
					ShowGoogle: googleEnabled,
				})

				return
			}
			logger.ErrorContext(r.Context(), "error creating player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		sessions.Set(w, player.ID)
		http.Redirect(w, r, landingPathFor(player.Role), http.StatusSeeOther)
	})
}

// claimOrCreatePlayer is the storage-side branch of HandleRegisterSubmit. If
// the request already has a session pointing at an anonymous (no
// password_hash) row, it upgrades that row via ClaimPlayer so the visitor
// keeps their player_id. Otherwise — no session, deleted row, or
// already-claimed row — it falls back to CreatePlayer.
//
// The "already claimed" fallback handles a concurrent registration race
// gracefully: by the time we call ClaimPlayer the row may have been claimed
// by a different request that shared the same cookie. Falling back to
// CreatePlayer means the user still completes registration, just without
// their pre-claim history attached.
//
// Wrapped errors keep the underlying ErrUsernameTaken / ErrPlayerAlreadyClaimed
// sentinel intact for [errors.Is] checks while satisfying wrapcheck.
func claimOrCreatePlayer(
	r *http.Request,
	players PlayerStore,
	sessions *session.Manager,
	username, passwordHash, requestedRole string,
) (*Player, error) {
	playerID, ok := sessions.PlayerID(r)
	if !ok {
		return createPlayerWrapped(r, players, username, passwordHash, requestedRole)
	}

	existing, err := players.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, username, passwordHash, requestedRole)
		}

		return nil, fmt.Errorf("get player by id for claim: %w", err)
	}
	if !existing.IsAnonymous() {
		// Session already belongs to a credentialled account. Treat this as a
		// fresh registration so the new account is created independently;
		// the existing session is replaced when the caller resets the cookie.
		return createPlayerWrapped(r, players, username, passwordHash, requestedRole)
	}

	claimed, err := players.ClaimPlayer(r.Context(), playerID, username, passwordHash, requestedRole)
	if err != nil {
		if errors.Is(err, ErrPlayerAlreadyClaimed) || errors.Is(err, ErrPlayerNotFound) {
			return createPlayerWrapped(r, players, username, passwordHash, requestedRole)
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
	username, passwordHash, requestedRole string,
) (*Player, error) {
	p, err := players.CreatePlayer(r.Context(), username, passwordHash, requestedRole)
	if err != nil {
		return nil, fmt.Errorf("create player: %w", err)
	}

	return p, nil
}

// HandleLoginForm returns a handler for GET /login that renders the login form.
// registrationEnabled controls whether the template shows the "No account? Register" link.
// googleEnabled controls whether the "Sign in with Google" button is rendered.
func HandleLoginForm(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	registrationEnabled, googleEnabled bool,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.render(w, r, http.StatusOK, formData{
			Title:        "Log in",
			ShowRegister: registrationEnabled,
			ShowGoogle:   googleEnabled,
		})
	})
}

// HandleLoginSubmit returns a handler for POST /login. It verifies the
// credentials, signs the player in, and redirects to the admin landing page.
// registrationEnabled controls whether error renders show the "No account? Register" link.
// googleEnabled controls whether error renders show the "Sign in with Google" button.
func HandleLoginSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
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

		sessions.Set(w, player.ID)
		http.Redirect(w, r, landingPathFor(player.Role), http.StatusSeeOther)
	})
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
	// Cleaned is the username with surrounding whitespace removed. Callers use it for
	// both storage and lookup so `" alice "` and `"alice"` cannot be treated as different users.
	Cleaned string
	// ErrMsg is a user-facing error message, populated only when OK is false.
	ErrMsg string
	// OK reports whether the inputs are valid.
	OK bool
}

// validateRegisterInput trims the username and validates the inputs.
func validateRegisterInput(username, password string) registerInput {
	cleaned := strings.TrimSpace(username)
	if cleaned == "" {
		return registerInput{Cleaned: cleaned, ErrMsg: "Username is required.", OK: false}
	}
	if len(password) < MinPasswordLength {
		return registerInput{
			Cleaned: cleaned,
			ErrMsg:  fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength),
			OK:      false,
		}
	}
	if len(password) > MaxPasswordLength {
		// bcrypt rejects passwords above MaxPasswordLength; catching it here
		// turns a wrapped 500 into a normal form-validation error with a
		// user-friendly message.
		return registerInput{
			Cleaned: cleaned,
			ErrMsg:  fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLength),
			OK:      false,
		}
	}

	return registerInput{Cleaned: cleaned, OK: true}
}

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
