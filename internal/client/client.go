// Package client provides a handler for serving the client files.
package client

import (
	"embed"
	"fmt"
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

	return http.StripPrefix("/client", http.FileServer(http.FS(noDirFS{fsys})))
}

// noDirFS wraps an [fs.FS] so [http.FileServer] returns 404 for a directory
// instead of generating a browsable index that would list the raw template
// fragments under static/partials/.
type noDirFS struct {
	fsys fs.FS
}

// Open returns [fs.ErrNotExist] for a directory and otherwise delegates.
func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open client asset %q: %w", name, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("stat client asset %q: %w", name, err)
	}
	if info.IsDir() {
		_ = f.Close()

		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return f, nil
}
