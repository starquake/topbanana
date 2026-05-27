//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestPWA_Integration covers ticket #466 — the installable shell wires
// up a web app manifest at /manifest.webmanifest, a service worker at
// /sw.js (root-scoped), the maskable + plain icons under /assets/, and
// every layout's <head> carries the manifest + apple-touch-icon links
// plus the SW registration snippet.
func TestPWA_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	t.Run("manifest is served at root with manifest mime", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+"/manifest.webmanifest")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "manifest+json") && !strings.Contains(ct, "json") {
			t.Errorf("Content-Type = %q, want a JSON-flavoured manifest media type", ct)
		}

		body := readAllString(t, resp.Body)
		for _, want := range []string{
			`"name"`,
			// Non-production deploys prefix the name with their env
			// label (e.g. "[development] Top Banana!"). Match the
			// stable suffix so the assertion stays valid in both.
			`Top Banana!"`,
			`"short_name"`,
			`"start_url"`,
			`"display"`,
			`"standalone"`,
			`"theme_color"`,
			`"background_color"`,
			`/assets/banana-192.png`,
			`/assets/banana-512.png`,
			`/assets/banana-maskable-512.png`,
			`"maskable"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("manifest body missing %q", want)
			}
		}
	})

	t.Run("service worker is served at root with js mime", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+"/sw.js")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "application/javascript"; !strings.HasPrefix(got, want) {
			t.Errorf("Content-Type = %q, want prefix %q", got, want)
		}
		if got, want := resp.Header.Get("Service-Worker-Allowed"), "/"; got != want {
			t.Errorf("Service-Worker-Allowed = %q, want %q", got, want)
		}

		body := readAllString(t, resp.Body)
		if strings.Contains(body, "__CACHE_VERSION__") {
			t.Error("sw.js still contains the cache-version placeholder; server-side substitution missed")
		}
		for _, want := range []string{
			"caches.open",
			"PRECACHE_URLS",
			"skipWaiting",
			"clients.claim",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("sw.js missing expected token %q", want)
			}
		}
	})

	t.Run("png icons are served with image/png", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{
			"/assets/banana-192.png",
			"/assets/banana-512.png",
			"/assets/banana-maskable-512.png",
		} {
			assertPNGAsset(ctx, t, srv.BaseURL+path)
		}
	})

	t.Run("home page links manifest and registers the service worker", func(t *testing.T) {
		t.Parallel()
		assertPWAHeadMarkup(ctx, t, srv.BaseURL+"/")
	})

	t.Run("auth login page links manifest and registers the service worker", func(t *testing.T) {
		t.Parallel()
		assertPWAHeadMarkup(ctx, t, srv.BaseURL+"/login")
	})

	t.Run("player client links manifest and registers the service worker", func(t *testing.T) {
		t.Parallel()
		assertPWAHeadMarkup(ctx, t, srv.BaseURL+"/client/")
	})
}

func assertPWAHeadMarkup(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`rel="manifest"`,
		`href="/manifest.webmanifest"`,
		`rel="apple-touch-icon"`,
		`href="/assets/banana-192.png"`,
		`navigator.serviceWorker.register('/sw.js')`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q", url, want)
		}
	}
}

func assertPNGAsset(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("%s status = %d, want %d", url, got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "image/png"; !strings.HasPrefix(got, want) {
		t.Errorf("%s Content-Type = %q, want prefix %q", url, got, want)
	}
}

func readAllString(t *testing.T, body io.Reader) string {
	t.Helper()

	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(out)
}
