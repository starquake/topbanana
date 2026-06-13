package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/starquake/topbanana/cmd/server/app"
)

// TestMkMediaDir_CreatesMissing pins that the startup helper creates the
// media directory (and any missing parents) so the first upload does not race a
// missing root (#936).
func TestMkMediaDir_CreatesMissing(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "media")
	if err := MkMediaDir(dir); err != nil {
		t.Fatalf("MkMediaDir err = %v, want nil", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created dir err = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("created path is not a directory")
	}
}

// TestMkMediaDir_ExistingDirIsFine pins that an already-present directory is
// not an error: a restart against a populated volume must succeed.
func TestMkMediaDir_ExistingDirIsFine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := MkMediaDir(dir); err != nil {
		t.Errorf("MkMediaDir on existing dir err = %v, want nil", err)
	}
}

// TestMkMediaDir_RejectsEmpty pins that an empty MediaDir is a
// misconfiguration: media would have nowhere to land, so the helper fails fast.
func TestMkMediaDir_RejectsEmpty(t *testing.T) {
	t.Parallel()

	if err := MkMediaDir(""); !errors.Is(err, ErrEmptyMediaDir) {
		t.Errorf("MkMediaDir(\"\") err = %v, want ErrEmptyMediaDir", err)
	}
}
