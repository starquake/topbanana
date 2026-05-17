// Package web provides the embedded admin/auth assets and a handler that
// serves them. Templates live alongside in internal/web/tmpl; the static
// assets (Tailwind output) live in internal/web/static and are mounted at
// /assets/ by the server router.
package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"

	"github.com/starquake/topbanana/internal/config"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an [http.Handler] that serves the admin/auth static assets
// at /assets/. The Tailwind output is committed and embedded, so there is no
// dev-directory override — regenerate via `make tailwind` (or `make
// tailwind-watch` + rebuild the binary) during development.
func Handler(cfg *config.Config) http.Handler {
	fsys, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(fsys))

	if cfg.IsProduction() {
		m := minify.New()
		m.AddFunc("text/css", css.Minify)
		m.AddFunc("application/javascript", js.Minify)
		m.AddFunc("text/javascript", js.Minify)

		return http.StripPrefix("/assets", m.Middleware(fileServer))
	}

	return http.StripPrefix("/assets", fileServer)
}
