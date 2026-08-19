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

//go:embed static/* tmpl/*
var clientFS embed.FS

// subFS returns the named subtree of this package: the on-disk copy under
// cfg.ClientDir when that dev override is set, the embedded copy otherwise.
func subFS(cfg *config.Config, dir string) fs.FS {
	var root fs.FS = clientFS
	if cfg.ClientDir != "" {
		root = os.DirFS(cfg.ClientDir)
	}
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic(err)
	}

	return sub
}

// Handler returns an [http.Handler] that serves the client files.
// If cfg.ClientDir is not empty, it serves static/ from that directory.
func Handler(cfg *config.Config) http.Handler {
	return http.StripPrefix("/client", http.FileServer(http.FS(noDirFS{subFS(cfg, "static")})))
}

// noDirFS wraps an [fs.FS] so [http.FileServer] returns 404 for a directory
// instead of generating a browsable index of the served tree.
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
