package render_test

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/starquake/topbanana/internal/csrf"
	. "github.com/starquake/topbanana/internal/render"
)

// newTestTemplate parses a tiny in-memory tree with a "base.gohtml" layout and
// a named partial so the tests exercise the renderer without touching the real
// template FS. The placeholder funcs mirror what production templates register
// at parse time and the renderer rebinds per request.
func newTestTemplate(t *testing.T) *template.Template {
	t.Helper()

	funcs := template.FuncMap{
		"csrfToken": func() string { return "" },
		"greeting":  func() string { return "" },
	}
	tmpl := template.Must(template.New("base.gohtml").Funcs(funcs).Parse(
		`base[{{greeting}}|{{csrfToken}}|{{.Body}}]`,
	))
	template.Must(tmpl.New("fragment").Parse(`frag[{{.Body}}]`))

	return tmpl
}

func TestRender_WritesStatusContentTypeAndBody(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	// A nil *csrf.Manager exercises the nil-csrf path: {{csrfToken}} resolves
	// to "" rather than calling the manager.
	renderer := New(logger, (*csrf.Manager)(nil), newTestTemplate(t), "base.gohtml", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	renderer.Render(rec, req, http.StatusTeapot, struct{ Body string }{Body: "hi"})

	if got, want := rec.Code, http.StatusTeapot; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	// greeting (no per-request func) and csrfToken (nil manager) both resolve
	// to the empty placeholder, so the body is base[|<csrf>|hi] with csrf "".
	if got, want := rec.Body.String(), "base[||hi]"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRender_BindsPerRequestFuncs(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	funcs := func(*http.Request) template.FuncMap {
		return template.FuncMap{"greeting": func() string { return "hello" }}
	}
	renderer := New(logger, (*csrf.Manager)(nil), newTestTemplate(t), "base.gohtml", funcs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	renderer.Render(rec, req, http.StatusOK, struct{ Body string }{Body: "x"})

	if got, want := rec.Body.String(), "base[hello||x]"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRenderPartial_ExecutesNamedTemplateWith200(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	renderer := New(logger, (*csrf.Manager)(nil), newTestTemplate(t), "base.gohtml", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	renderer.RenderPartial(rec, req, "fragment", struct{ Body string }{Body: "z"})

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rec.Body.String(), "frag[z]"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRender_LogsErrorOnBadData(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	renderer := New(logger, (*csrf.Manager)(nil), newTestTemplate(t), "base.gohtml", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	// .Body on an int payload has no such field, so ExecuteTemplate errors
	// after the header is written; the renderer logs rather than returns.
	renderer.Render(rec, req, http.StatusOK, 42)

	if got, want := buf.String(), "error executing template"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
}

func TestParse_ParsesGlobsAndPageWithFuncs(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		// The layout references a parse-time func and a template the page
		// defines - a forward reference resolved at execution, so parsing the
		// layout before the page is fine.
		"layouts/base.gohtml": &fstest.MapFile{
			Data: []byte(`{{define "base.gohtml"}}base[{{greeting}}|{{template "frag" .}}]{{end}}`),
		},
		"pages/page.gohtml": &fstest.MapFile{
			Data: []byte(`{{define "frag"}}frag[{{.Body}}]{{end}}`),
		},
	}
	funcs := template.FuncMap{"greeting": func() string { return "hi" }}

	tmpl := Parse(fsys, funcs, "pages/page.gohtml", "layouts/*.gohtml")

	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "base.gohtml", struct{ Body string }{Body: "x"}); err != nil {
		t.Fatalf("ExecuteTemplate err = %v, want nil", err)
	}
	if got, want := buf.String(), "base[hi|frag[x]]"; got != want {
		t.Errorf("Parse output = %q, want %q", got, want)
	}
}
