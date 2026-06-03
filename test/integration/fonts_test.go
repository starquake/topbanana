package integration_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestSelfHostedFonts covers ticket #599: Inter and Orbitron must be
// served from the app rather than fetched from Google's CDN at page
// load, so the pages render offline with no external request.
func TestSelfHostedFonts(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL
	client := &http.Client{}

	// One representative Inter file and one Orbitron file. A 200 plus the
	// woff2 content-type proves the embedded fonts/ tree is served at
	// /assets/fonts/ and that .woff2 resolves to font/woff2 even on the
	// distroless production image (which has no /etc/mime.types).
	fontFiles := []string{
		"/assets/fonts/inter-latin.woff2",
		"/assets/fonts/orbitron-latin.woff2",
	}
	for _, path := range fontFiles {
		t.Run("served "+path, func(t *testing.T) {
			t.Parallel()
			resp := httpGet(ctx, t, client, baseURL+path)
			defer closeBody(t, resp.Body)

			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := resp.Header.Get("Content-Type"), "font/woff2"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
		})
	}

	// The rendered pages must not pull fonts from Google's CDN, and the
	// stylesheet must declare the self-hosted @font-face rules pointing at
	// /assets/fonts/. Together these prove the CDN <link> tags were
	// removed and the local fonts wired in via _tailwind.css.
	t.Run("pages reference no font CDN", func(t *testing.T) {
		t.Parallel()
		pages := []string{
			baseURL + "/",
			baseURL + "/login",
			baseURL + "/client/",
		}
		bannedSubstrings := []string{
			"fonts.googleapis.com",
			"fonts.gstatic.com",
		}
		for _, page := range pages {
			body := getBody(ctx, t, page)
			for _, banned := range bannedSubstrings {
				if strings.Contains(body, banned) {
					t.Errorf("page %s still references %q (#599: self-host fonts)", page, banned)
				}
			}
		}
	})

	t.Run("app.css declares self-hosted font-face", func(t *testing.T) {
		t.Parallel()
		css := getBody(ctx, t, baseURL+"/assets/css/app.css")

		if got, want := css, "@font-face"; !strings.Contains(got, want) {
			t.Errorf("app.css missing %q", want)
		}
		if got, want := css, "/assets/fonts/"; !strings.Contains(got, want) {
			t.Errorf("app.css missing local font path %q", want)
		}
	})
}
