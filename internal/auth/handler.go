package auth

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

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
}

// HandleRegisterForm returns a handler for GET /register that renders the
// registration form.
func HandleRegisterForm(logger *slog.Logger) http.Handler {
	render := newTemplateRenderer(logger, "auth/pages/register.gohtml")

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
	players PlayerStore,
	sessions *session.Manager,
	adminUsernames []string,
) http.Handler {
	render := newTemplateRenderer(logger, "auth/pages/register.gohtml")

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
func HandleLoginForm(logger *slog.Logger) http.Handler {
	render := newTemplateRenderer(logger, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		render.render(w, r, http.StatusOK, formData{Title: "Log in"})
	})
}

// HandleLoginSubmit returns a handler for POST /login. It verifies the
// credentials, signs the player in, and redirects to the admin landing page.
func HandleLoginSubmit(
	logger *slog.Logger,
	players PlayerStore,
	sessions *session.Manager,
) http.Handler {
	render := newTemplateRenderer(logger, "auth/pages/login.gohtml")

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
				renderInvalidCredentials(render, w, r, username)

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
			renderInvalidCredentials(render, w, r, username)

			return
		}

		if err := CheckPassword(player.PasswordHash, password); err != nil {
			renderInvalidCredentials(render, w, r, username)

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

func renderInvalidCredentials(render *templateRenderer, w http.ResponseWriter, r *http.Request, username string) {
	render.render(w, r, http.StatusUnauthorized, formData{
		Title:    "Log in",
		Username: username,
		Message:  "Invalid username or password.",
	})
}

// templateRenderer renders a template combined with the auth layouts.
type templateRenderer struct {
	logger *slog.Logger
	t      *template.Template
}

func newTemplateRenderer(logger *slog.Logger, page string) *templateRenderer {
	layouts := template.Must(template.ParseFS(tmpl.FS, "auth/layouts/*.gohtml"))

	return &templateRenderer{
		logger: logger,
		t:      template.Must(template.Must(layouts.Clone()).ParseFS(tmpl.FS, page)),
	}
}

func (tr *templateRenderer) render(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.WriteHeader(status)
	if err := tr.t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing template", slog.Any("err", err))
	}
}
