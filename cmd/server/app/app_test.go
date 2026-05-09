package app_test

import (
	"bytes"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
)

// TestMain wires goose's global state up once for the package — calling
// SetupGoose from parallel tests races on the goose package-level fields.
func TestMain(m *testing.M) {
	database.SetupGoose()
	m.Run()
}

func TestCheck_FreshDB_Succeeds(t *testing.T) {
	t.Parallel()

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		return map[string]string{"DB_URI": dbURI, "PORT": "0"}[key]
	}

	var stdout bytes.Buffer
	if err := app.Check(t.Context(), getenv, &stdout); err != nil {
		t.Fatalf("Check err = %v, want nil", err)
	}
	if got, want := stdout.String(), "startup ok"; !strings.Contains(got, want) {
		t.Errorf("stdout should contain %q, got %q", want, got)
	}
}

func TestCheck_BadDBURI_ReturnsError(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		// A path under a nonexistent directory: SQLite will fail to open it.
		return map[string]string{"DB_URI": "file:/nonexistent-dir/topbanana.sqlite", "PORT": "0"}[key]
	}

	var stdout bytes.Buffer
	err := app.Check(t.Context(), getenv, &stdout)
	if err == nil {
		t.Fatal("Check err = nil, want non-nil for unreachable DB_URI")
	}
}
