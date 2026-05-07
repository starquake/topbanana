package auth

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// MinPasswordLength is the minimum number of bytes required for a password.
const MinPasswordLength = 13

const adminLandingPath = "/admin/quizzes"

// formData is the data passed to the register and login templates.
type formData struct {
	Title    string
	Username string
	Message  string
	// ShowRegister controls whether the login template renders the
	// "No account? Register" link. False when REGISTRATION_ENABLED is unset/false.
	ShowRegister bool
}

// HandleRegisterForm returns a handler for GET /register that renders the
// registration form.
func HandleRegisterForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.render(w, r, http.StatusOK, formData{Title: "Register"})
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
func HandleRegisterSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	adminUsernames []string,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/register.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				Title:    "Register",
				Username: input.Cleaned,
				Message:  input.ErrMsg,
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

		player, err := players.CreatePlayer(r.Context(), input.Cleaned, hashed, role)
		if err != nil {
			if errors.Is(err, ErrUsernameTaken) {
				render.render(w, r, http.StatusConflict, formData{
					Title:    "Register",
					Username: input.Cleaned,
					Message:  "Username is already taken.",
				})

				return
			}
			logger.ErrorContext(r.Context(), "error creating player", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		sessions.Set(w, player.ID)
		http.Redirect(w, r, adminLandingPath, http.StatusSeeOther)
	})
}

// HandleLoginForm returns a handler for GET /login that renders the login form.
// registrationEnabled controls whether the template shows the "No account? Register" link.
func HandleLoginForm(logger *slog.Logger, csrfMgr *csrf.Manager, registrationEnabled bool) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.render(w, r, http.StatusOK, formData{Title: "Log in", ShowRegister: registrationEnabled})
	})
}

// HandleLoginSubmit returns a handler for POST /login. It verifies the
// credentials, signs the player in, and redirects to the admin landing page.
// registrationEnabled controls whether error renders show the "No account? Register" link.
func HandleLoginSubmit(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players PlayerStore,
	sessions *session.Manager,
	registrationEnabled bool,
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
				renderInvalidCredentials(render, w, r, username, registrationEnabled)

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
			renderInvalidCredentials(render, w, r, username, registrationEnabled)

			return
		}

		if err := CheckPassword(player.PasswordHash, password); err != nil {
			renderInvalidCredentials(render, w, r, username, registrationEnabled)

			return
		}

		sessions.Set(w, player.ID)
		http.Redirect(w, r, adminLandingPath, http.StatusSeeOther)
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
		return registerInput{Cleaned: cleaned, ErrMsg: "Password must be at least 13 characters.", OK: false}
	}

	return registerInput{Cleaned: cleaned, OK: true}
}

func renderInvalidCredentials(
	render *templateRenderer,
	w http.ResponseWriter,
	r *http.Request,
	username string,
	registrationEnabled bool,
) {
	render.render(w, r, http.StatusUnauthorized, formData{
		Title:        "Log in",
		Username:     username,
		Message:      "Invalid username or password.",
		ShowRegister: registrationEnabled,
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
	funcs := template.FuncMap{
		"csrfToken": func() string { return "" },
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
	})

	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}
