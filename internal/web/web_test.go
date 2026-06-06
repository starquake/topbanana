package web_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/web"
)

// TestHandler_DefaultServesEmbeddedFS pins the production default: with
// WebStaticDir empty, Handler serves the [embed.FS] tree so the binary
// stays self-contained.
func TestHandler_DefaultServesEmbeddedFS(t *testing.T) {
	t.Parallel()

	h := web.Handler(&config.Config{AppEnvironment: "development"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/css/app.css", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (embedded /assets/css/app.css should always be present)", got, want)
	}
	if rr.Body.Len() == 0 {
		t.Error("body is empty; expected the embedded Tailwind output")
	}
}

// TestHandler_WebStaticDirServesOnDisk pins the dev override: a file
// written to WebStaticDir is the one Handler serves, not the embedded
// version. The on-disk content is a sentinel string that cannot appear
// in the committed Tailwind output, so the assertion can't be fooled
// by an accidental embed-FS hit.
func TestHandler_WebStaticDirServesOnDisk(t *testing.T) {
	t.Parallel()

	staticDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticDir, "css"), 0o755); err != nil {
		t.Fatalf("MkdirAll err = %v, want nil", err)
	}
	sentinel := "/* web-static-dir-override-sentinel */"
	if err := os.WriteFile(filepath.Join(staticDir, "css", "app.css"), []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile err = %v, want nil", err)
	}

	h := web.Handler(&config.Config{AppEnvironment: "development", WebStaticDir: staticDir})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/css/app.css", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), sentinel; !strings.Contains(got, want) {
		t.Errorf("body should contain sentinel %q (override not honoured)", want)
	}
}

// TestServiceWorkerHandler_SubstitutesCacheVersion checks the
// placeholder substitution: the served SW must not still contain the
// __CACHE_VERSION__ token and must include a 12-char hex tag in its
// place. Driven against the embedded FS so the production code path
// is what's exercised.
func TestServiceWorkerHandler_SubstitutesCacheVersion(t *testing.T) {
	t.Parallel()

	h := web.ServiceWorkerHandler(&config.Config{AppEnvironment: "development"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sw.js", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Content-Type"), "application/javascript"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rr.Header().Get("Service-Worker-Allowed"), "/"; got != want {
		t.Errorf("Service-Worker-Allowed = %q, want %q", got, want)
	}
	body := rr.Body.String()
	if got, want := body, "__CACHE_VERSION__"; strings.Contains(got, want) {
		t.Errorf("body still contains placeholder %q (substitution missed)", want)
	}
	if got, want := body, "topbanana-shell-"; !strings.Contains(got, want) {
		t.Errorf("body missing %q (cache-name template not preserved)", want)
	}
}

// TestServiceWorkerHandler_DevModeRecomputesVersion pins the dev-loop
// invariant: when WebStaticDir is set, the cache version must reflect
// the on-disk asset bytes per request, so editing app.css (or any
// other precached shell asset) yields a fresh SW immediately. The
// production embed path is covered by the previous test.
func TestServiceWorkerHandler_DevModeRecomputesVersion(t *testing.T) {
	t.Parallel()

	staticDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticDir, "css"), 0o755); err != nil {
		t.Fatalf("MkdirAll err = %v, want nil", err)
	}
	swSrc := "const CACHE_VERSION = '__CACHE_VERSION__';"
	if err := os.WriteFile(filepath.Join(staticDir, "sw.js"), []byte(swSrc), 0o600); err != nil {
		t.Fatalf("WriteFile sw.js err = %v, want nil", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "css", "app.css"), []byte("/* v1 */"), 0o600); err != nil {
		t.Fatalf("WriteFile app.css v1 err = %v, want nil", err)
	}

	h := web.ServiceWorkerHandler(&config.Config{AppEnvironment: "development", WebStaticDir: staticDir})

	first := serveAndReadBody(t, h, "/sw.js")
	if got, want := first, "__CACHE_VERSION__"; strings.Contains(got, want) {
		t.Fatalf("first body still contains %q", want)
	}

	if err := os.WriteFile(filepath.Join(staticDir, "css", "app.css"), []byte("/* v2 changed */"), 0o600); err != nil {
		t.Fatalf("WriteFile app.css v2 err = %v, want nil", err)
	}
	second := serveAndReadBody(t, h, "/sw.js")
	if got, want := second, first; got == want {
		t.Errorf("dev-mode SW response did not change after asset edit; both bodies = %q", got)
	}
}

// TestManifestHandler_ServesManifestMime pins the Content-Type and
// the no-cache header so a redeploy that updates the manifest is not
// stuck behind heuristic browser caching.
func TestManifestHandler_ServesManifestMime(t *testing.T) {
	t.Parallel()

	h := web.ManifestHandler(&config.Config{AppEnvironment: "development"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/manifest.webmanifest", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Content-Type"), "application/manifest+json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rr.Header().Get("Cache-Control"), "no-cache"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := rr.Body.String(), `"Top Banana!"`; !strings.Contains(got, want) {
		t.Errorf("body missing %q", want)
	}
	// __ENV_TITLE_TAG__ must be substituted (or stripped) - leaking the
	// placeholder into the served JSON would corrupt the install prompt.
	if got, want := rr.Body.String(), "__ENV_TITLE_TAG__"; strings.Contains(got, want) {
		t.Errorf("body still contains placeholder %q", want)
	}
}

// TestManifestHandler_InjectsEnvTag pins the non-production tagging:
// when envtag.Set has stamped a value, the manifest's name and
// short_name prefix it so the install prompt shows the env at a
// glance instead of looking like production.
//
//nolint:paralleltest // Mutates the process-wide envtag.label; running in parallel with the sibling manifest test would race.
func TestManifestHandler_InjectsEnvTag(t *testing.T) {
	envtag.Set("[staging] ")
	t.Cleanup(func() { envtag.Set("") })

	h := web.ManifestHandler(&config.Config{AppEnvironment: "staging"})
	body := serveAndReadBody(t, h, "/manifest.webmanifest")

	if got, want := body, `"[staging] Top Banana!"`; !strings.Contains(got, want) {
		t.Errorf("body missing %q", want)
	}
	if got, want := body, `"[staging] Top Banana!"`; !strings.Contains(got, want) {
		t.Errorf("body missing %q", want)
	}
}

// TestShellAssetPathsMatchPrecacheURLs pins the cache-busting invariant: the
// SW cache version is hashed over shellAssetPaths(), so it only changes when
// one of those files changes. If a URL is precached but not in shellAssetPaths
// (or vice versa), a bundle rename could ship without invalidating the cache
// and clients would keep serving the stale precached copy. shellAssetPaths is
// the precache list normalized off the served URLs, plus sw.js (the SW hashes
// its own source so a precache-list edit alone busts the cache).
func TestShellAssetPathsMatchPrecacheURLs(t *testing.T) {
	t.Parallel()

	swBody, err := fs.ReadFile(web.ExportEmbeddedStaticFS(), "sw.js")
	if err != nil {
		t.Fatalf("ReadFile sw.js err = %v, want nil", err)
	}
	precache := precacheAssetPaths(t, string(swBody))
	precache = append(precache, "sw.js")
	slices.Sort(precache)

	shell := web.ExportShellAssetPaths()
	slices.Sort(shell)

	if got, want := strings.Join(shell, ","), strings.Join(precache, ","); got != want {
		t.Errorf(
			"shellAssetPaths() out of sync with PRECACHE_URLS:\n  shellAssetPaths = %q\n  precache+sw.js  = %q",
			got, want,
		)
	}
}

// precacheAssetPaths parses the PRECACHE_URLS array out of sw.js and normalizes
// each entry to an embedded-FS path (drop the leading slash and the optional
// /assets/ mount prefix) so it can be compared to shellAssetPaths().
func precacheAssetPaths(t *testing.T, swBody string) []string {
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

	var paths []string
	for entry := range strings.SplitSeq(after[:end], ",") {
		entry = strings.Trim(strings.TrimSpace(entry), "'\"")
		if entry == "" {
			continue
		}
		entry = strings.TrimPrefix(entry, "/")
		entry = strings.TrimPrefix(entry, "assets/")
		paths = append(paths, entry)
	}

	return paths
}

func serveAndReadBody(t *testing.T, h http.Handler, target string) string {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", target, got, want)
	}

	return rr.Body.String()
}
