// Package profile renders the signed-in player's profile page at
// GET /profile and handles the rename submission at POST
// /profile/username (#410). The page is the future home for
// account-level controls: email change (depends on #111), password
// change (depends on #112), linked OAuth identities, etc. Today it
// hosts the username editor only; everything else is scoped out of
// the initial cut.
//
// Authorisation lives entirely in auth.RequireAuthenticated upstream
// of the handler - both routes are mounted behind it so the handler
// can assume a *Player is on the request context.
package profile

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// maxFormBodySize caps the rename POST body. 16 KiB is generous for
// a single username field + csrf token; mirrors the pattern in
// internal/auth/handler.go.
const maxFormBodySize = 16 * 1024

// pageData feeds profile.gohtml. Title flows into the auth layout's
// <title>. Username is the value pre-filled into the input. Message
// surfaces server-side validation errors (taken username, empty
// input, etc.). Saved is true on a successful POST so the template
// can show a small confirmation banner.
type pageData struct {
	Title    string
	Username string
	Message  string
	Saved    bool
}

// HandleProfile returns the [http.Handler] for GET /profile. The
// auth.RequireAuthenticated middleware mounted upstream guarantees
// the request context carries the signed-in player.
func HandleProfile(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			// Defence in depth: the middleware should have written a
			// player to the context. If it did not, 500 - the
			// alternative is a blank form that cannot save.
			logger.ErrorContext(r.Context(), "profile handler reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		render.render(w, r, http.StatusOK, pageData{
			Title:    "Profile",
			Username: player.Username,
		})
	})
}

// HandleProfileUsername returns the [http.Handler] for POST
// /profile/username. Parses the form, calls RenamePlayer, and
// re-renders the page with either the new username + a success
// banner or the old username + an error banner.
//
// The store enforces the UNIQUE-on-username constraint atomically,
// so a concurrent rename to the same target by another player
// produces a clean ErrUsernameTaken without any application-side
// race. ErrUsernameEmpty is mapped to a 400 with the same form;
// ErrUsernameTaken to a 409. Anything else is a 500.
func HandleProfileUsername(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players auth.PlayerStore,
) http.Handler {
	render := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "profile rename reached without a player in context")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormBodySize)
		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "error parsing profile form", slog.Any("err", err))
			http.Error(w, "bad form", http.StatusBadRequest)

			return
		}

		raw := r.PostFormValue("username")
		cleaned := strings.TrimSpace(raw)

		updated, err := players.RenamePlayer(r.Context(), player.ID, cleaned)
		if err != nil {
			renderRenameError(render, w, r, player.Username, raw, err)

			return
		}

		render.render(w, r, http.StatusOK, pageData{
			Title:    "Profile",
			Username: updated.Username,
			Saved:    true,
		})
	})
}

// renderRenameError maps a store error to the right HTTP status +
// user-facing message and re-renders the form with the user's
// attempted value (so they can fix a typo without retyping). Falls
// through to a plain 500 for unexpected errors so the operator's
// log gets the full stack instead of a misleading form banner.
func renderRenameError(
	render *templateRenderer,
	w http.ResponseWriter,
	r *http.Request,
	currentUsername, attempted string,
	err error,
) {
	switch {
	case errors.Is(err, auth.ErrUsernameEmpty):
		render.render(w, r, http.StatusBadRequest, pageData{
			Title:    "Profile",
			Username: currentUsername,
			Message:  "Username is required.",
		})
	case errors.Is(err, auth.ErrUsernameTaken):
		render.render(w, r, http.StatusConflict, pageData{
			Title:    "Profile",
			Username: attempted,
			Message:  "That name is already taken. Pick a different one.",
		})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// templateRenderer mirrors the auth package's helper of the same
// name - parses the auth layout + the page template once and re-
// clones per request so html/template's context-aware escaping
// applies cleanly. csrfToken is bound per-render so the form's
// hidden input always carries a token paired with the response's
// nonce cookie.
type templateRenderer struct {
	logger *slog.Logger
	csrf   *csrf.Manager
	t      *template.Template
}

func newTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, page string) *templateRenderer {
	funcs := template.FuncMap{
		"csrfToken": func() string { return "" },
		"ogImage":   func() string { return "" },
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

func (tr *templateRenderer) render(w http.ResponseWriter, r *http.Request, status int, data pageData) {
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.gohtml", data); err != nil {
		tr.logger.ErrorContext(r.Context(), "error executing profile template", slog.Any("err", err))
	}
}
