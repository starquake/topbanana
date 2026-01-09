// Package client provides a handler for serving the client files.
package client

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"

	"github.com/starquake/topbanana/internal/config"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an [http.Handler] that serves the client files.
// If cfg.ClientDir is not empty, it serves files from that directory.
// If cfg.isProduction is true, it minifies the files.
func Handler(cfg *config.Config) http.Handler {
	var fsys fs.FS
	if cfg.ClientDir != "" {
		fsys = os.DirFS(cfg.ClientDir)
	} else {
		var err error
		fsys, err = fs.Sub(staticFS, "static")
		if err != nil {
			panic(err)
		}
	}

	fileServer := http.FileServer(http.FS(fsys))

	if cfg.IsProduction() {
		m := minify.New()
		m.AddFunc("text/html", html.Minify)
		m.AddFunc("text/css", css.Minify)
		m.AddFunc("application/javascript", js.Minify)
		m.AddFunc("text/javascript", js.Minify)

		return http.StripPrefix("/client", m.Middleware(fileServer))
	}

	return http.StripPrefix("/client", fileServer)
}
