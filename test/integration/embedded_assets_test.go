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
		// Web / admin: every served script is a built bundle under dist/ or a
		// vendored lib; the standalone admin/auth scripts are bundled too. The
		// player client loads Alpine + anime from this same web-served vendor
		// dir rather than a per-client duplicate.
		"/static/css/app.css",
		"/static/js/dist/host-bigscreen.js",
		"/static/js/dist/share.js",
		"/static/js/dist/quiz-reorder.js",
		"/static/js/dist/cooldown.js",
		"/static/js/dist/copy-prompt.js",
		"/static/js/dist/password-length.js",
		"/static/js/htmx.min.js",
		"/static/js/vendor/alpine.min.js",
		"/static/js/vendor/anime.umd.min.js",
		"/static/js/vendor/sortable.min.js",
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
		// Alpine + anime are no longer duplicated under the client tree; the
		// client shells load the web-served copies at /static/js/vendor/.
		"/client/js/vendor/alpine.min.js",
		"/client/js/vendor/anime.umd.min.js",
		// Web sources relocated to frontend/web: the esbuild entries and the
		// now-bundled standalone scripts are no longer served at their old
		// un-bundled paths.
		"/static/js/host-bigscreen.js",
		"/static/js/share.js",
		"/static/js/quiz-reorder.js",
		"/static/js/cooldown.js",
		"/static/js/copy-prompt.js",
		"/static/js/password-length.js",
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
