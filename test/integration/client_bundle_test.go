package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestClientBundles pins the esbuild client-bundle pipeline (#721): the player
// shells load the bundled entry points, the bundles are served from the
// embedded FS, and the shared share.js dialog module is inlined into the
// bundle (slice 2) rather than fetched cross-tree from /static/js/ at runtime.
func TestClientBundles(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL
	client := &http.Client{}

	t.Run("shells load the bundled entry points", func(t *testing.T) {
		t.Parallel()

		for path, want := range map[string]string{
			"/client/": `<script type="module" src="/client/js/dist/app.js"></script>`,
			"/join":    `<script type="module" src="/client/js/dist/join.js"></script>`,
		} {
			body := getBody(ctx, t, baseURL+path)
			if !strings.Contains(body, want) {
				t.Errorf("%s shell missing bundled entry %q (#721)", path, want)
			}
			// The pre-bundle source entry must no longer be referenced -
			// the individual modules are inlined into the bundle now.
			if banned := `src="/client/js/app.js"`; strings.Contains(body, banned) {
				t.Errorf("%s shell still references the unbundled source entry %q (#721)", path, banned)
			}
		}
	})

	for _, bundle := range []string{"app", "join"} {
		t.Run("bundle "+bundle+".js is served", func(t *testing.T) {
			t.Parallel()
			resp := httpGet(ctx, t, client, baseURL+"/client/js/dist/"+bundle+".js")
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
		})
	}

	// share.js moved into the shared source tree (frontend/shared) in slice 2
	// and is inlined into the client bundle, so the app bundle must NOT carry a
	// runtime import of /static/js/share.js any more - the dialog code travels
	// inside the bundle. The inlined share-dialog markup confirms it is present.
	t.Run("app bundle inlines share.js instead of importing it cross-tree", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/client/js/dist/app.js")
		if banned := "/static/js/share.js"; strings.Contains(body, banned) {
			t.Errorf(
				"app bundle still carries the cross-tree import %q - share.js must be inlined in slice 2 (#721)",
				banned,
			)
		}
		if want := "share-dialog"; !strings.Contains(body, want) {
			t.Errorf(
				"app bundle missing inlined share dialog markup %q - share.js should be bundled in (#721)",
				want,
			)
		}
	})
}
