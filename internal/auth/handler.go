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

	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// MinPasswordLength is the minimum number of bytes required for a password.
const MinPasswordLength = 13

const adminLandingPath = "/admin/quizzes"

// dummyHashOnce computes a bcrypt hash once on first use. The login handler
// runs CheckPassword against this hash when the username does not exist, so
// the response time matches the case where the username does exist. This
// prevents an attacker from enumerating valid usernames by response timing.
//
//nolint:gochecknoglobals // package-level cache for a single dummy hash, by design
var dummyHashOnce = sync.OnceValue(func() string {
	h, err := HashPassword("timing-oracle-dummy-do-not-use")
	if err != nil {
		panic("dummyHashOnce: HashPassword on constant input failed")
	}

	return h
})

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
// The first registered player is promoted to admin. Subsequent registrants matching
// `adminUsernames` (case-sensitive) are also promoted. Everyone else gets RolePlayer.
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

		username, msg, ok := validateRegisterInput(rawUsername, password)
		if !ok {
			render.render(w, r, http.StatusBadRequest, formData{
				Title:    "Register",
				Username: username,
				Message:  msg,
			})

			return
		}

		role, err := decideRole(r.Context(), players, username, adminUsernames)
		if err != nil {
			logger.ErrorContext(r.Context(), "error deciding role", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		hashed, err := HashPassword(password)
		if err != nil {
			logger.ErrorContext(r.Context(), "error hashing password", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		player, err := players.CreatePlayer(r.Context(), username, hashed, role)
		if err != nil {
			if errors.Is(err, ErrUsernameTaken) {
				render.render(w, r, http.StatusConflict, formData{
					Title:    "Register",
					Username: username,
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing login form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		username := r.PostFormValue("username")
		password := r.PostFormValue("password")

		player, err := players.GetPlayerByUsername(r.Context(), username)
		if err != nil {
			if errors.Is(err, ErrPlayerNotFound) {
				// Equalise timing with the valid-username path so an attacker
				// cannot enumerate usernames by response time.
				_ = CheckPassword(dummyHashOnce(), password)
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
			_ = CheckPassword(dummyHashOnce(), password)
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

// validateRegisterInput trims the username and validates the inputs. It returns
// the trimmed username, an error message for the user, and ok indicating whether
// the inputs are valid. Callers use the trimmed username for both storage and
// lookup so `" alice "` and `"alice"` cannot be treated as different users.
//
//nolint:nonamedreturns // named results disambiguate two same-type strings (revive: confusing-results)
func validateRegisterInput(username, password string) (cleaned, errMsg string, ok bool) {
	cleaned = strings.TrimSpace(username)
	if cleaned == "" {
		return cleaned, "Username is required.", false
	}
	if len(password) < MinPasswordLength {
		return cleaned, "Password must be at least 13 characters.", false
	}

	return cleaned, "", true
}

func decideRole(
	ctx context.Context,
	players PlayerStore,
	username string,
	adminUsernames []string,
) (string, error) {
	count, err := players.CountPlayers(ctx)
	if err != nil {
		return "", fmt.Errorf("count players: %w", err)
	}
	if count == 0 {
		return RoleAdmin, nil
	}
	if slices.Contains(adminUsernames, username) {
		return RoleAdmin, nil
	}

	return RolePlayer, nil
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
