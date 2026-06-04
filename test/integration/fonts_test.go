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

	// One representative Inter file and one Orbitron file. The 200 guards
	// that the embedded fonts/ tree is still served at /assets/fonts/ (an
	// embed or route regression would 404 here). The content-type pins
	// font/woff2 on the test host. Note: this does NOT prove the distroless
	// production case - this host's /etc/mime.types already maps .woff2, so
	// the assertion passes even without the explicit mime.AddExtensionType
	// registration in web.go; that distroless fix is comment-guarded there,
	// not test-guarded.
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

	// The core offline guard: the rendered pages must not pull fonts from
	// Google's CDN. Catches a re-added <link rel="stylesheet"
	// href="fonts.googleapis.com..."> in any of the three shared base
	// layouts (web home/auth/admin) or the client shell.
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
}

// TestFontPreloadLinks covers ticket #691: each surface that renders the
// self-hosted fonts must declare <link rel="preload" as="font"> for the
// above-the-fold subsets so the browser fetches them before parsing
// app.css. Without the preload the first paint falls back to a system
// font and the custom face only appears on a refresh (the service worker
// has it cached by then). The extended-latin subset is deliberately NOT
// preloaded - it is rarely used and preloading an unused font triggers a
// browser console warning. This test pins the served HTML, which is the
// only place the preload is observable; the FOUT itself is a timing flash
// and not reliably assertable.
func TestFontPreloadLinks(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL

	wantLinks := []string{
		`<link rel="preload" href="/assets/fonts/inter-latin.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel="preload" href="/assets/fonts/orbitron-latin.woff2" as="font" type="font/woff2" crossorigin>`,
	}

	assertPreloads := func(t *testing.T, body string) {
		t.Helper()
		for _, want := range wantLinks {
			if !strings.Contains(body, want) {
				t.Errorf("body missing font preload %q (#691); body=%q", want, body)
			}
		}
		// The extended-latin subset must not be preloaded - it is rarely
		// needed and preloading an unused font wastes bandwidth and warns.
		if banned := `rel="preload" href="/assets/fonts/inter-latin-ext.woff2"`; strings.Contains(body, banned) {
			t.Errorf("body preloads the extended-latin subset (%q), which should not be preloaded (#691)", banned)
		}
	}

	// Home layout (public) and auth layout (public /login) need no session.
	t.Run("home layout", func(t *testing.T) {
		t.Parallel()
		assertPreloads(t, getBody(ctx, t, baseURL+"/"))
	})
	t.Run("auth layout", func(t *testing.T) {
		t.Parallel()
		assertPreloads(t, getBody(ctx, t, baseURL+"/login"))
	})

	// Admin layout requires an authenticated admin session. The first
	// registrant is promoted to admin, matching the other admin tests.
	t.Run("admin layout", func(t *testing.T) {
		t.Parallel()
		adminClient := authClient(t)
		registerVerifyAndMint(ctx, t, adminClient, baseURL, srv.DBURI, "preload-admin", "preload-admin-pass-123")
		assertPreloads(t, getOK(ctx, t, adminClient, baseURL+"/admin"))
	})
}
