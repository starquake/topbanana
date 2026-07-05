package demo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoArchives is returned by [ArchivePaths] when the directory holds no .zip
// files, so a misconfigured mount or a wrong path fails fast rather than
// silently seeding nothing.
var ErrNoArchives = errors.New("no demo archives (.zip) found in directory")

// ArchivePaths returns the full paths of every *.zip file in dir, sorted by
// filename so the demo set restores in a stable order ([os.ReadDir] returns
// entries sorted by name). It errors with [ErrNoArchives] (wrapped with dir)
// when the directory holds no .zip files. Callers read the bytes or open a
// zip reader as they need.
func ArchivePaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read demo archive dir: %w", err)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoArchives, dir)
	}

	return paths, nil
}
