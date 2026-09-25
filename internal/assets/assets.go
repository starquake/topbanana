// Package assets provides the embedded static assets and a handler that
// serves them. The served tree (Tailwind output, fonts, vendored JS,
// PWA icons) lives in internal/assets/static and is mounted at /static/
// by the server router. The mount is surface-agnostic: the player client,
// admin, host, auth, and home pages all load their assets from it.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/envtag"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an [http.Handler] that serves the static assets at
// /static/. Defaults to the committed [embed.FS] so production binaries
// ship self-contained. When [config.Config.WebStaticDir] is set (development
// only - see config.Parse), the on-disk directory is served instead so a
// `make tailwind` regen is visible on the next request without a binary
// restart. Mirrors the CLIENT_DIR override for the player-client half.
func Handler(cfg *config.Config) http.Handler {
	// The distroless production image has no /etc/mime.types, so
	// [mime.TypeByExtension](".woff2") would return empty and
	// [http.FileServer] would content-sniff the vendored fonts (#599) as
	// application/octet-stream. Register the type explicitly so the woff2
	// content-type is correct regardless of the runtime image. The error
	// is discarded because AddExtensionType only fails on a malformed type
	// string, and "font/woff2" is a constant valid one.
	_ = mime.AddExtensionType(".woff2", "font/woff2")

	fsys := resolveStaticFS(cfg)
	// The embedded tree is immutable, so its ETags are hashed once; an on-disk
	// dev tree changes under us and relies on Last-Modified instead.
	var etags map[string]string
	if cfg.WebStaticDir == "" {
		etags = contentETags(fsys)
	}
	files := http.FileServer(http.FS(noDirFS{fsys: fsys}))

	return http.StripPrefix("/static", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Asset URLs are not fingerprinted, so a browser must revalidate each
		// use; the ETag turns that into a 304.
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := etags[strings.TrimPrefix(path.Clean(r.URL.Path), "/")]; ok {
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	}))
}

// contentETags maps every file in the embedded tree to a strong ETag over its
// contents. [http.FileServer] answers a matching If-None-Match from the header.
func contentETags(fsys fs.FS) map[string]string {
	etags := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(fsys, name)
		if rerr != nil {
			return fmt.Errorf("read static asset %q: %w", name, rerr)
		}
		sum := sha256.Sum256(b)
		etags[name] = strconv.Quote(hex.EncodeToString(sum[:etagHashBytes]))

		return nil
	})
	if err != nil {
		// Like fs.Sub in resolveStaticFS, reading the embedded tree can only
		// fail if the embed declaration was tampered with.
		panic(err)
	}

	return etags
}

// etagHashBytes is how much of the SHA-256 goes into an asset ETag; 128 bits
// is ample to tell releases apart.
const etagHashBytes = 16

// noDirFS wraps an [fs.FS] so [http.FileServer] returns 404 for a directory
// instead of generating a browsable index of the served tree.
type noDirFS struct {
	fsys fs.FS
}

// Open returns [fs.ErrNotExist] for a directory and otherwise delegates.
func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open static asset %q: %w", name, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("stat static asset %q: %w", name, err)
	}
	if info.IsDir() {
		_ = f.Close()

		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return f, nil
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
		// __ENV_TITLE_TAG__ lets non-production deploys surface their
		// environment in the installed-app name without needing a
		// separate manifest file per env. Production leaves it empty
		// so the install prompt and home-screen icon read cleanly.
		out := strings.ReplaceAll(string(body), "__ENV_TITLE_TAG__", envtag.Get())
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, out)
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
// cover every page (a SW served from /static/sw.js can only intercept
// fetches under /static/).
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
// internal/assets/static/) that determine the SW cache version. Must
// stay in sync with PRECACHE_URLS in sw.js minus the leading
// "/static/" prefix.
func shellAssetPaths() []string {
	return []string{
		"manifest.webmanifest",
		"sw.js",
		"css/app.css",
		"fonts/inter-latin.woff2",
		"fonts/inter-latin-ext.woff2",
		"fonts/orbitron-latin.woff2",
		"js/htmx.min.js",
		"js/dist/share.js",
		"js/vendor/alpine.min.js",
		"js/vendor/anime.umd.min.js",
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
