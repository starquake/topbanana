// Package client provides a handler for serving the client files.
package client

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/starquake/topbanana/internal/config"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an [http.Handler] that serves the client files.
// If cfg.ClientDir is not empty, it serves files from that directory.
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

	return http.StripPrefix("/client", http.FileServer(http.FS(fsys)))
}
