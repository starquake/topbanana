package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestVendoredClientLibraries covers ticket #295 — the player client must
// not load anime.js or Alpine.js from an external CDN at runtime, and the
// vendored copies must actually be served from the embedded FS. The test
// fails if either guard regresses: a stray cdn.jsdelivr.net reference
// would mean the player picks up whatever the CDN decides to serve at
// that moment (with the player's session cookie attached), and a 404 on
// the vendor path would mean the SPA loads with no JS at all.
func TestVendoredClientLibraries(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL
	client := &http.Client{}

	t.Run("SPA HTML references no external script CDN", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/client/")

		// Scripts loaded from any third-party origin would be visible
		// as `src="https://...` or `src="//...` in the rendered HTML.
		// The vendored libraries use root-relative paths, so the only
		// legitimate `src=` values start with `/`.
		bannedSubstrings := []string{
			`src="https://`,
			`src='https://`,
			`src="//`,
			`src='//`,
			"cdn.jsdelivr.net",
		}
		for _, banned := range bannedSubstrings {
			if strings.Contains(body, banned) {
				t.Errorf("SPA body still references external script %q (#295: bundle, don't CDN)", banned)
			}
		}
	})

	t.Run("vendored anime.js is served", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, client, baseURL+"/static/js/vendor/anime.umd.min.js")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("vendored Alpine.js is served", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, client, baseURL+"/static/js/vendor/alpine.min.js")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}
