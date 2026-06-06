package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestClientBundles pins the esbuild client-bundle pipeline (#721, slice 1):
// the player shells load the bundled entry points, the bundles are served
// from the embedded FS, and the one cross-tree module (share.js) stays an
// external runtime import rather than being inlined into the bundle.
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

	// share.js lives in the web tree (/assets/js/), not the client tree, so
	// slice 1 keeps it as an external import the browser fetches at runtime.
	// The bundle must still carry the import statement; if a future change
	// inlines it, this guard flags that slice-2 work landed early.
	t.Run("app bundle keeps share.js as an external import", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/client/js/dist/app.js")
		if want := "/assets/js/share.js"; !strings.Contains(body, want) {
			t.Errorf(
				"app bundle missing external import %q - share.js must stay a runtime import in slice 1 (#721)",
				want,
			)
		}
	})
}
