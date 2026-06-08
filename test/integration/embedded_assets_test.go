package integration_test

import (
	"context"
	"net/http"
	"testing"
)

// TestEmbeddedAssets_ServeOnlyBuiltOutput pins #756: the build-time JS/CSS
// source now lives under frontend/ and only the served, built output is
// embedded under internal/*/static. So the esbuild bundles, vendored libs,
// HTML shells, and raw-served scripts are reachable (200), while a request for
// a source module 404s because it is no longer in the embedded tree. Runs
// against the embedded FS (the harness leaves CLIENT_DIR / WEB_STATIC_DIR
// unset), so this guards the production embed, not a dev DirFS override.
func TestEmbeddedAssets_ServeOnlyBuiltOutput(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	served := []string{
		// Player client.
		"/client/",
		"/join",
		"/client/js/dist/app.js",
		"/client/js/dist/join.js",
		"/client/js/vendor/alpine.min.js",
		// Web / admin: every served script is a built bundle under dist/ or a
		// vendored lib; the standalone admin/auth scripts are bundled too.
		"/assets/css/app.css",
		"/assets/js/dist/host-lobby.js",
		"/assets/js/dist/share.js",
		"/assets/js/dist/quiz-reorder.js",
		"/assets/js/dist/cooldown.js",
		"/assets/js/dist/copy-prompt.js",
		"/assets/js/dist/password-length.js",
		"/assets/js/htmx.min.js",
		"/assets/js/vendor/sortable.min.js",
	}
	for _, path := range served {
		assertEmbedStatus(ctx, t, srv.BaseURL+path, http.StatusOK)
	}

	notServed := []string{
		// Client source modules relocated to frontend/client.
		"/client/js/app.js",
		"/client/js/join.js",
		"/client/js/components/GameApp.js",
		"/client/js/services/api.js",
		// Web sources relocated to frontend/web: the esbuild entries and the
		// now-bundled standalone scripts are no longer served at their old
		// un-bundled paths.
		"/assets/js/host-lobby.js",
		"/assets/js/share.js",
		"/assets/js/quiz-reorder.js",
		"/assets/js/cooldown.js",
		"/assets/js/copy-prompt.js",
		"/assets/js/password-length.js",
	}
	for _, path := range notServed {
		assertEmbedStatus(ctx, t, srv.BaseURL+path, http.StatusNotFound)
	}
}

func assertEmbedStatus(ctx context.Context, t *testing.T, url string, want int) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got := resp.StatusCode; got != want {
		t.Errorf("GET %s status = %d, want %d", url, got, want)
	}
}
