// Package web provides the embedded admin/auth assets and a handler that
// serves them. Templates live alongside in internal/web/tmpl; the static
// assets (Tailwind output) live in internal/web/static and are mounted at
// /assets/ by the server router.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

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
