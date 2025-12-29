package api_test

import (
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestAdmin_Integration(t *testing.T) {
	t.Parallel()

	ctx, _ := testutil.SignalCtx(t)

	var err error
	// Setup temporary database for the test
	tmpDB, err := os.CreateTemp(t.TempDir(), "topbanana-test-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmpDBPath := tmpDB.Name()
	err = tmpDB.Close()
	if err != nil {
		t.Fatalf("failed to close temp db: %v", err)
	}
	defer func() {
		removeErr := os.Remove(tmpDBPath)
		if removeErr != nil {
			t.Errorf("failed to remove temp db: %s", removeErr)
		}
	}()

	getenv := func(key string) string {
		env := map[string]string{
			"PORT":   "0", // Let the OS choose an available port
			"DB_URI": "file:" + tmpDBPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		}

		return env[key]
	}

	pr, pw := io.Pipe()
	defer func(pr *io.PipeReader) {
		closeErr := pr.Close()
		if closeErr != nil {
			t.Errorf("failed to close pipe reader: %v", closeErr)
		}
	}(pr)
	defer func(pw *io.PipeWriter) {
		closeErr := pw.Close()
		if closeErr != nil {
			t.Errorf("failed to close pipe writer: %v", closeErr)
		}
	}(pw)

	go func() {
		appErr := app.Run(ctx, getenv, pw)
		if appErr != nil {
			t.Errorf("error running server: %v", appErr)

			return
		}
	}()

	serverAddr := testutil.ServerAddress(t, pr)
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("http://%s/healthz", serverAddr))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}
}
