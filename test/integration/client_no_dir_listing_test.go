package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestClientHandler_NoDirectoryListing pins that the static client handler
// returns 404 for a directory path instead of listing its contents, while a
// named asset stays reachable (#7).
func TestClientHandler_NoDirectoryListing(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, dir := range []string{"/client/js/", "/client/js/dist/"} {
		resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+dir)
		body := readAllString(t, resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Body.Close err = %v, want nil", err)
		}
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("GET %s status = %d, want %d (directory listing must be disabled)", dir, got, want)
		}
		// A directory index would list child file names; assert none leak.
		if strings.Contains(body, "<a href=") {
			t.Errorf("GET %s returned a directory listing: %q", dir, body)
		}
	}

	// A named asset under the same tree stays reachable.
	resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+"/client/js/dist/app.js")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET /client/js/dist/app.js status = %d, want %d (named asset must stay reachable)", got, want)
	}
}

// TestClientHandler_TemplateSourceNotServed pins #1284: the shell templates
// live outside the served tree, so no request reaches their raw source. Before
// the move they sat in static/ and GET /client/join.html answered 200 with
// unrendered {{ ... }} actions.
func TestClientHandler_TemplateSourceNotServed(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	paths := []string{
		"/client/join.html",
		"/client/join.gohtml",
		"/client/partials/round_intro.html",
		"/client/partials/round_intro.gohtml",
		"/client/tmpl/index.gohtml",
		"/client/tmpl/partials/brand_mark.gohtml",
	}
	for _, p := range paths {
		resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+p)
		body := readAllString(t, resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Body.Close err = %v, want nil", err)
		}
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("GET %s status = %d, want %d (template source must not be served)", p, got, want)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("GET %s leaked unrendered template source: %q", p, body)
		}
	}
}
