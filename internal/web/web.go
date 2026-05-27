// Package web provides the embedded admin/auth assets and a handler that
// serves them. Templates live alongside in internal/web/tmpl; the static
// assets (Tailwind output) live in internal/web/static and are mounted at
// /assets/ by the server router.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"

	"github.com/starquake/topbanana/internal/config"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an [http.Handler] that serves the admin/auth static assets
// at /assets/. Defaults to the committed [embed.FS] so production binaries
// ship self-contained. When [config.Config.WebStaticDir] is set (development
// only — see config.Parse), the on-disk directory is served instead so a
// `make tailwind` regen is visible on the next request without a binary
// restart. Mirrors the CLIENT_DIR override for the player-client half.
func Handler(cfg *config.Config) http.Handler {
	fileServer := http.FileServer(http.FS(resolveStaticFS(cfg)))

	if cfg.IsProduction() {
		m := minify.New()
		m.AddFunc("text/css", css.Minify)
		m.AddFunc("application/javascript", js.Minify)
		m.AddFunc("text/javascript", js.Minify)

		return http.StripPrefix("/assets", m.Middleware(fileServer))
	}

	return http.StripPrefix("/assets", fileServer)
}

// ManifestHandler serves /manifest.webmanifest with the correct
// Content-Type. The file lives in static/ alongside the other embedded
// assets; this handler exists separately from [Handler] because the
// manifest must be served at the site root for the PWA install prompt
// to honour the default scope of "/". The no-cache header keeps a
// redeploy that updates the manifest (new icon, theme colour) from
// being stuck behind the browser's heuristic cache.
func ManifestHandler(cfg *config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := fs.ReadFile(resolveStaticFS(cfg), "manifest.webmanifest")
		if err != nil {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}

// ServiceWorkerHandler serves /sw.js with the cache version placeholder
// substituted. The version is the first 12 hex chars of a SHA-256 over
// the concatenated bytes of every shell asset the SW precaches, so any
// change to those assets between releases yields a fresh cache name
// and the install handler discards the previous version on activate.
//
// In production the embedded FS is immutable, so the version is
// computed once at handler construction. In dev mode (WebStaticDir
// set) the version is recomputed per request so a `make tailwind`
// regen or any other on-disk asset edit triggers a fresh SW and
// invalidates the precache without a manual unregister.
//
// The SW must be served from the site root for its default scope to
// cover every page (a SW served from /assets/sw.js can only intercept
// fetches under /assets/).
func ServiceWorkerHandler(cfg *config.Config) http.Handler {
	embeddedVersion := ""
	if cfg.WebStaticDir == "" {
		embeddedVersion = computeCacheVersion(resolveStaticFS(cfg))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fsys := resolveStaticFS(cfg)
		version := embeddedVersion
		if version == "" {
			version = computeCacheVersion(fsys)
		}
		body, err := fs.ReadFile(fsys, "sw.js")
		if err != nil {
			http.NotFound(w, r)

			return
		}
		out := strings.ReplaceAll(string(body), "__CACHE_VERSION__", version)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = io.WriteString(w, out)
	})
}

// cacheVersionHexChars is the prefix length taken off the SHA-256 of
// the shell assets to produce a short, human-readable cache tag.
// Twelve hex chars is enough to make collisions across releases
// effectively impossible while staying short enough to read in
// devtools.
const cacheVersionHexChars = 12

// shellAssetPaths returns the list of static-asset paths (relative to
// internal/web/static/) that determine the SW cache version. Must
// stay in sync with PRECACHE_URLS in sw.js minus the leading
// "/assets/" prefix.
func shellAssetPaths() []string {
	return []string{
		"manifest.webmanifest",
		"sw.js",
		"css/app.css",
		"js/htmx.min.js",
		"js/share.js",
		"banana.svg",
		"banana-192.png",
		"banana-512.png",
		"banana-maskable-512.png",
		"og-image.png",
	}
}

// computeCacheVersion hashes the contents of every shell asset to
// derive a stable version token. Assets that fail to open are skipped
// rather than panicking so a partially-broken filesystem still yields
// a working SW (the affected request will 404 on its own).
func computeCacheVersion(fsys fs.FS) string {
	h := sha256.New()
	for _, name := range shellAssetPaths() {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			continue
		}
		_, _ = h.Write([]byte(name))
		_, _ = h.Write(b)
	}

	return hex.EncodeToString(h.Sum(nil))[:cacheVersionHexChars]
}

// resolveStaticFS picks the static-asset filesystem: an on-disk
// [os.DirFS] when WebStaticDir is set, otherwise the embedded tree.
// Pulled out of Handler so the branching reads as a single line and a
// future override (e.g. test-supplied [fs.FS]) has one place to land.
func resolveStaticFS(cfg *config.Config) fs.FS {
	if cfg.WebStaticDir != "" {
		return os.DirFS(cfg.WebStaticDir)
	}
	fsys, err := fs.Sub(staticFS, "static")
	if err != nil {
		// fs.Sub on a static //go:embed path can only fail at build time;
		// reaching here means the embed declaration was tampered with.
		panic(err)
	}

	return fsys
}
