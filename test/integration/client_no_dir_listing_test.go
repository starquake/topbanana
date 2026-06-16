package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestClientHandler_NoDirectoryListing pins that the static client handler
// returns 404 for a directory path instead of listing the template fragments,
// while a named asset stays reachable (#7).
func TestClientHandler_NoDirectoryListing(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, dir := range []string{"/client/partials/", "/client/js/", "/client/js/dist/"} {
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
