// Package render renders HTML pages from a parsed template tree. It is shared
// by every server-rendered surface (admin, auth, profile, host): it clones the
// tree per request, binds the csrfToken func plus any surface-specific
// per-request funcs, and writes the page, so the clone/csrf/execute mechanics
// live in one place.
package render

import (
	"html/template"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
)

// Parse parses the shared layout/partial globs (with funcs registered as
// parse-time placeholders) and the page template into one tree, ready to wrap
// in a Renderer. It is the parse half of the template plumbing every surface
// shares: the base globs are parsed first, then cloned and the page parsed into
// the clone so the base stays page-free. globs are passed straight to
// [template.Template.ParseFS]; funcs must cover every func the templates
// reference at parse time.
func Parse(fsys fs.FS, funcs template.FuncMap, page string, globs ...string) *template.Template {
	base := template.Must(template.New("").Funcs(funcs).ParseFS(fsys, globs...))

	return template.Must(template.Must(base.Clone()).ParseFS(fsys, page))
}

// PerRequestFuncs returns the surface-specific template funcs to bind for a
// single request (e.g. the admin top bar's viewerName / navSection / isAdmin,
// resolved from the request context). The Renderer always binds csrfToken
// itself, so implementations need not. May be nil for a surface that needs
// nothing beyond csrfToken.
type PerRequestFuncs func(r *http.Request) template.FuncMap

// Renderer renders one parsed template tree. The CSRF manager may be nil for
// callers that render error pages without an embedded form (the placeholder
// {{csrfToken}} still resolves to "").
type Renderer struct {
	logger *slog.Logger
	csrf   *csrf.Manager
	t      *template.Template
	layout string
	funcs  PerRequestFuncs
}

// New wraps an already-parsed template tree. layout is the template name Render
// executes (e.g. "base.gohtml"); funcs may be nil.
func New(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	t *template.Template,
	layout string,
	funcs PerRequestFuncs,
) *Renderer {
	return &Renderer{logger: logger, csrf: csrfMgr, t: t, layout: layout, funcs: funcs}
}

// Render renders the configured layout with the supplied data and status. It
// does not return an error: by the time ExecuteTemplate runs the headers are
// written (prepare's csrf token call sets the nonce cookie), so an error page
// is no longer an option - failures are logged.
func (re *Renderer) Render(w http.ResponseWriter, r *http.Request, status int, data any) {
	t, ok := re.prepare(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, re.layout, data); err != nil {
		re.logger.ErrorContext(
			r.Context(),
			"error executing template",
			slog.String("template", re.layout),
			slog.Any("err", err),
		)
	}
}

// RenderPartial executes a named template (typically a partial) with 200
// instead of the full layout - for HTMX handlers swapping a fragment.
func (re *Renderer) RenderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := re.prepare(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		re.logger.ErrorContext(
			r.Context(),
			"error executing partial template",
			slog.String("name", name),
			slog.Any("err", err),
		)
	}
}

// prepare clones the tree and binds csrfToken plus the surface's per-request
// funcs. Returns the prepared tree and true; on clone failure it surfaces 500
// and returns false. The csrf.Token call writes a header (the nonce cookie),
// so callers must defer their own header writes until prepare returns.
func (re *Renderer) prepare(w http.ResponseWriter, r *http.Request) (*template.Template, bool) {
	t, err := re.t.Clone()
	if err != nil {
		re.logger.ErrorContext(r.Context(), "error cloning template", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return nil, false
	}
	csrfToken := ""
	if re.csrf != nil {
		csrfToken = re.csrf.Token(w, r)
	}
	funcs := template.FuncMap{"csrfToken": func() string { return csrfToken }}
	if re.funcs != nil {
		maps.Copy(funcs, re.funcs(r))
	}

	return t.Funcs(funcs), true
}
