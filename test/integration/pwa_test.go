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

	t.Run("every precached URL is served", func(t *testing.T) {
		t.Parallel()

		swBody := getBody(ctx, t, srv.BaseURL+"/sw.js")
		urls := parsePrecacheURLs(t, swBody)
		// A bundle rename (e.g. share.js -> dist/share.js) that updated
		// PRECACHE_URLS but not the served path would leave the SW caching a
		// 404 forever; fetch each precache entry and require a 200 so the
		// precache <-> served-bundle correspondence can never silently drift.
		if got, want := len(urls), 13; got != want {
			t.Errorf("parsed %d precache URLs, want %d - update this guard if PRECACHE_URLS changed", got, want)
		}
		for _, path := range urls {
			assertServed200(ctx, t, srv.BaseURL+path)
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

	// The player shells (solo + live) additionally carry the standalone
	// iOS PWA meta tags and viewport-fit=cover that the web/auth layouts
	// don't need (#826). A standalone home-screen launch and the
	// safe-area rendering itself are iOS-Safari-specific and not
	// reproducible in the Chromium/Firefox e2e, so pin the served markup
	// here.
	t.Run("solo client serves the standalone PWA meta tags", func(t *testing.T) {
		t.Parallel()
		assertStandalonePWAMarkup(ctx, t, srv.BaseURL+"/client/")
	})

	t.Run("join shell serves the standalone PWA meta tags", func(t *testing.T) {
		t.Parallel()
		assertStandalonePWAMarkup(ctx, t, srv.BaseURL+"/join")
	})
}

// assertStandalonePWAMarkup pins the head markup an installable iOS PWA needs
// (#826): viewport-fit=cover so env(safe-area-inset-*) resolves non-zero, the
// Apple + standards-track standalone-capable tags, the translucent status-bar
// style, and the app title. The safe-area .player-shell padding itself is
// covered by the committed app.css; only its trigger lives in the head.
func assertStandalonePWAMarkup(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`viewport-fit=cover`,
		`name="apple-mobile-web-app-capable" content="yes"`,
		`name="mobile-web-app-capable" content="yes"`,
		`name="apple-mobile-web-app-status-bar-style" content="black-translucent"`,
		`name="apple-mobile-web-app-title"`,
		`class="player-shell`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q", url, want)
		}
	}
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

// assertServed200 fetches url and requires a 200, closing the body. Used to
// confirm every precached path resolves to a real served asset.
func assertServed200(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("%s status = %d, want %d", url, got, want)
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

// parsePrecacheURLs pulls the quoted paths out of the PRECACHE_URLS array in
// the served sw.js so the precache list is verified against the live server
// rather than a hand-copied table that could rot.
func parsePrecacheURLs(t *testing.T, swBody string) []string {
	t.Helper()

	const marker = "PRECACHE_URLS = ["
	_, after, ok := strings.Cut(swBody, marker)
	if !ok {
		t.Fatalf("sw.js does not contain %q", marker)
	}
	end := strings.Index(after, "]")
	if end < 0 {
		t.Fatal("sw.js PRECACHE_URLS array is not closed")
	}

	var urls []string
	for entry := range strings.SplitSeq(after[:end], ",") {
		entry = strings.TrimSpace(entry)
		entry = strings.Trim(entry, "'\"")
		if entry != "" {
			urls = append(urls, entry)
		}
	}

	return urls
}

func readAllString(t *testing.T, body io.Reader) string {
	t.Helper()

	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(out)
}
