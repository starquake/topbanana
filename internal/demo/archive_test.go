package demo_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/starquake/topbanana/internal/demo"
)

// TestArchivePaths pins the shared scan: it returns the full paths of the .zip
// files sorted by filename, skips non-.zip entries and subdirectories.
func TestArchivePaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.zip"))
	writeFile(t, filepath.Join(dir, "a.zip"))
	writeFile(t, filepath.Join(dir, "notes.txt"))
	if err := os.Mkdir(filepath.Join(dir, "sub.zip"), 0o755); err != nil {
		t.Fatalf("Mkdir() err = %v, want nil", err)
	}

	paths, err := demo.ArchivePaths(dir)
	if err != nil {
		t.Fatalf("ArchivePaths() err = %v, want nil", err)
	}

	wantPaths := []string{filepath.Join(dir, "a.zip"), filepath.Join(dir, "b.zip")}
	if got := len(paths); got != len(wantPaths) {
		t.Fatalf("len(paths) = %d, want %d (%v)", got, len(wantPaths), paths)
	}
	for i, p := range paths {
		if got, want := p, wantPaths[i]; got != want {
			t.Errorf("paths[%d] = %q, want %q", i, got, want)
		}
	}
}

// TestArchivePaths_EmptyDir pins the fail-fast guard: a directory with no .zip
// files errors with [demo.ErrNoArchives] rather than returning an empty slice.
func TestArchivePaths_EmptyDir(t *testing.T) {
	t.Parallel()

	_, err := demo.ArchivePaths(t.TempDir())
	if got, want := err, demo.ErrNoArchives; !errors.Is(got, want) {
		t.Errorf("ArchivePaths() err = %v, want %v", got, want)
	}
}

// TestArchivePaths_MissingDir surfaces a bad directory as a read error.
func TestArchivePaths_MissingDir(t *testing.T) {
	t.Parallel()

	_, err := demo.ArchivePaths(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("ArchivePaths() err = nil, want non-nil for a missing directory")
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v, want nil", path, err)
	}
}
