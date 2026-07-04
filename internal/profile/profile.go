// Package profile renders the signed-in player's profile page and the
// account-level controls it hosts:
//   - GET/POST /profile (display name editor, #410)
//   - GET/POST /profile/email (email change, #111)
//   - GET/POST /profile/password (password change, #112)
//
// Every route is mounted behind auth.RequireAuthenticated, so the handlers can
// assume a *Player is on the request context.
package profile

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/version"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// maxFormBodySize caps the rename POST body. 16 KiB is generous for
// a single displayName field + csrf token; mirrors the pattern in
// internal/auth/handler.go.
const maxFormBodySize = 16 * 1024

// pageData feeds profile.gohtml. Title flows into the auth layout's
// <title>. DisplayName is the value pre-filled into the input. Message
// surfaces server-side validation errors (taken display name, empty
// input, etc.). Saved is true on a successful POST so the template
// can show a small confirmation banner. Back* drive the form's
// return link so a visitor arriving from the admin chrome lands back
// on the dashboard instead of the public home page.
type pageData struct {
	Title       string
	DisplayName string
	Message     string
	Saved       bool
	BackHref    string
	BackLabel   string
	Next        string
}

// profileBack resolves the page's return link from the ?next= query
// param. Only an internal admin path is honoured (validated through
// auth.SafeNextPath, then gated to the /admin prefix so this stays a
// closed allowlist, not an open redirect); anything else falls back to
// the public home page. Next is echoed back so the POST re-render can
// preserve the return target across a validation error.
func profileBack(r *http.Request) (href, label, next string) {
	next = adminNextPath(r.URL.Query().Get("next"))
	href, label = backFromNext(locale.Resolve(r), next)

	return href, label, next
}

// adminNextPath returns raw when it is a safe same-site path under
// /admin, otherwise "". The auth.SafeNextPath guard rejects schemes,
// hosts, and backslash tricks; the prefix check then narrows the
// allowlist to the admin surface so no other internal path can ride
// the return link.
func adminNextPath(raw string) string {
	safe := auth.SafeNextPath(raw)
	if safe == "/admin" || strings.HasPrefix(safe, "/admin/") {
		return safe
	}

	return ""
}

// HandleProfile returns the [http.Handler] for GET /profile. The
// auth.RequireAuthenticated middleware mounted upstream guarantees
// the request context carries the signed-in player.
func HandleProfile(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile.gohtml")

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

		loc := locale.Resolve(r)
		backHref, backLabel, next := profileBack(r)
		renderer.render(w, r, http.StatusOK, pageData{
			Title:       locale.Translate(loc, "profile.heading"),
			DisplayName: player.DisplayName,
			BackHref:    backHref,
			BackLabel:   backLabel,
			Next:        next,
		})
	})
}

// HandleProfileDisplayName returns the [http.Handler] for POST
// /profile/display-name. Parses the form, calls RenamePlayer, and
// re-renders the page with either the new displayName + a success
// banner or the old displayName + an error banner.
//
// The store enforces the UNIQUE-on-displayName constraint atomically,
// so a concurrent rename to the same target by another player
// produces a clean ErrDisplayNameTaken without any application-side
// race. ErrDisplayNameEmpty is mapped to a 400 with the same form;
// ErrDisplayNameTaken to a 409. Anything else is a 500.
func HandleProfileDisplayName(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	players auth.PlayerStore,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/profile.gohtml")

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

		raw := r.PostFormValue("display_name")
		cleaned := strings.TrimSpace(raw)

		// The return target rides the POST as a hidden field so the
		// back link survives a re-render; re-validate it on the way in
		// rather than trusting the submitted value.
		next := adminNextPath(r.PostFormValue("next"))

		updated, err := players.RenamePlayer(r.Context(), player.ID, cleaned)
		if err != nil {
			renderRenameError(renderer, logger, w, r, renameAttempt{
				playerID:           player.ID,
				currentDisplayName: player.DisplayName,
				attempted:          raw,
				next:               next,
			}, err)

			return
		}

		loc := locale.Resolve(r)
		backHref, backLabel := backFromNext(loc, next)
		renderer.render(w, r, http.StatusOK, pageData{
			Title:       locale.Translate(loc, "profile.heading"),
			DisplayName: updated.DisplayName,
			Saved:       true,
			BackHref:    backHref,
			BackLabel:   backLabel,
			Next:        next,
		})
	})
}

// backFromNext maps an already-validated next path to the back link's
// href + localized label, defaulting to the public home page when next
// is empty.
func backFromNext(loc, next string) (href, label string) {
	if next != "" {
		return next, locale.Translate(loc, "profile.backToAdmin")
	}

	return "/", locale.Translate(loc, "profile.backToHome")
}

// renameAttempt carries the per-request context a failed rename needs
// to re-render the form: who tried, what they had, what they typed, and
// where their back link should point.
type renameAttempt struct {
	playerID           int64
	currentDisplayName string
	attempted          string
	next               string
}

// renderRenameError maps a store error to the right HTTP status +
// user-facing message and re-renders the form with the user's
// attempted value (so they can fix a typo without retyping). Falls
// through to a plain 500 for unexpected errors so the operator's
// log gets the full stack instead of a misleading form banner.
//
// Each branch logs: the two expected user errors at Info (a rejected
// rename otherwise leaves no server-side trace, so "why couldn't I
// change my name?" is undiagnosable), the unexpected branch at Error
// with the cause. The attempted name is logged for the taken case so
// the collision target is visible.
func renderRenameError(
	renderer *pageRenderer,
	logger *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
	a renameAttempt,
	err error,
) {
	loc := locale.Resolve(r)
	backHref, backLabel := backFromNext(loc, a.next)
	switch {
	case errors.Is(err, auth.ErrDisplayNameEmpty):
		logger.InfoContext(r.Context(), "profile rename rejected: empty name",
			slog.Int64("player_id", a.playerID))
		renderer.render(w, r, http.StatusBadRequest, pageData{
			Title:       locale.Translate(loc, "profile.heading"),
			DisplayName: a.currentDisplayName,
			Message:     locale.Translate(loc, "profile.displayNameRequired"),
			BackHref:    backHref,
			BackLabel:   backLabel,
			Next:        a.next,
		})
	case errors.Is(err, auth.ErrDisplayNameTaken):
		logger.InfoContext(r.Context(), "profile rename rejected: name taken",
			slog.Int64("player_id", a.playerID), slog.String("attempted", a.attempted))
		renderer.render(w, r, http.StatusConflict, pageData{
			Title:       locale.Translate(loc, "profile.heading"),
			DisplayName: a.attempted,
			Message:     locale.Translate(loc, "profile.displayNameTaken"),
			BackHref:    backHref,
			BackLabel:   backLabel,
			Next:        a.next,
		})
	default:
		logger.ErrorContext(r.Context(), "profile rename failed",
			slog.Int64("player_id", a.playerID), slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// pageRenderer wraps a render.Renderer in a typed render method so every
// profile call site passes a pageData, not an any. The shared renderer takes
// any, which would let a stray-typed payload through; this thin wrapper keeps
// the compile-time check the package relied on while reusing the shared
// clone/csrf/execute mechanics.
type pageRenderer struct {
	r *render.Renderer
}

func (pr *pageRenderer) render(w http.ResponseWriter, r *http.Request, status int, data pageData) {
	pr.r.Render(w, r, status, data)
}

// renderAny renders a page whose data type is not pageData (the email and
// password pages carry their own structs). Kept distinct from render so the
// typed wrapper stays the default call site and a stray any-typed payload at a
// pageData call site still fails the compiler.
func (pr *pageRenderer) renderAny(w http.ResponseWriter, r *http.Request, status int, data any) {
	pr.r.Render(w, r, status, data)
}

// parseTemplate parses the auth layouts plus the named page, registering the
// profile surface's parse-time placeholder funcs (the profile pages reuse the
// auth layouts). render rebinds the per-request funcs at execute time.
func parseTemplate(page string) *template.Template {
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
		"passwordHelp": func() string {
			return fmt.Sprintf("Must be %d-%d characters.", auth.MinPasswordLength, auth.MaxPasswordLength)
		},
		"passwordMinLength": func() int { return auth.MinPasswordLength },
		// Placeholder so the shared components glob parses; this surface
		// does not render quiz_card, which is the only user (#889).
		"humanizeTime": func(time.Time) string { return "" },
		// Parse-time placeholders for the shared client_footer's t/lang (#1115);
		// render.Renderer rebinds them per request.
		"t":    func(string) string { return "" },
		"lang": func() string { return locale.LocaleEN },
	}

	return render.Parse(tmpl.FS, funcs, page, "components/*.gohtml", "auth/layouts/*.gohtml")
}

// newTemplateRenderer parses the named page and wraps the tree in a
// render.Renderer (behind pageRenderer's typed render). It mirrors the auth
// package's helper of the same name. csrfToken is bound per render by
// render.Renderer.
func newTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, page string) *pageRenderer {
	return &pageRenderer{r: render.New(logger, csrfMgr, parseTemplate(page), "base.gohtml", profilePerRequestFuncs)}
}

// profilePerRequestFuncs binds the profile surface's per-request template
// funcs: the OG image URL and the viewer's display name / signed-in flag
// resolved from the request context. render.Renderer binds csrfToken itself, so
// it is omitted here. Matches the auth surface's per-request set.
func profilePerRequestFuncs(r *http.Request) template.FuncMap {
	displayName := ""
	signedIn := false
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		displayName = p.DisplayName
		signedIn = true
	}

	loc := locale.Resolve(r)

	return template.FuncMap{
		"ogImage":      func() string { return absurl.BaseURL(r) + "/static/og-image.png" },
		"viewerName":   func() string { return displayName },
		"isSignedIn":   func() bool { return signedIn },
		"passwordHelp": func() string { return localizedPasswordHelp(loc) },
	}
}

// localizedPasswordHelp renders the password length help text for loc,
// filling the {min}/{max} placeholders so it stays bound to the constants.
func localizedPasswordHelp(loc string) string {
	help := locale.Translate(loc, "common.passwordHelp")
	help = strings.ReplaceAll(help, "{min}", strconv.Itoa(auth.MinPasswordLength))

	return strings.ReplaceAll(help, "{max}", strconv.Itoa(auth.MaxPasswordLength))
}

// localizeCount translates key for loc and fills the {n} placeholder with n,
// keeping the format string constant so vet does not flag it.
func localizeCount(loc, key string, n int) string {
	return strings.ReplaceAll(locale.Translate(loc, key), "{n}", strconv.Itoa(n))
}
