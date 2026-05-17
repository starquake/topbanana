//go:build integration

package integration_test

import (
	"context"
	"errors"
	"maps"
	"net"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once for this package's tests.
	database.SetupGoose()
	m.Run()
}

// testServer is the addressable surface a started integration server
// exposes. BaseURL covers HTTP-driven tests; DBURI is only needed by tests
// that open their own *sql.DB for direct store access (e.g. gameplay).
type testServer struct {
	BaseURL string
	DBURI   string
}

// startServer boots a real server against an ephemeral port and a fresh
// test DB, waits until /healthz responds, and returns a context tied to
// the test's lifetime plus a testServer with the resulting URLs. Shutdown
// is registered via t.Cleanup — the server is stopped, errCh drained, and
// a non-Canceled exit fails the test.
//
// extraEnv is merged on top of the default getenv (HOST=localhost,
// PORT=0, DB_URI=<tmpdb>) so tests can opt in to flags like
// REGISTRATION_ENABLED without redoing the rest of the boilerplate.
func startServer(t *testing.T, extraEnv map[string]string) (context.Context, testServer) {
	t.Helper()

	ctx, stop := testutil.SignalCtx(t)
	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":   "localhost",
			"PORT":   "0",
			"DB_URI": dbURI,
		}
		maps.Copy(env, extraEnv)

		return env[key]
	}

	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(getenv("HOST"), getenv("PORT")))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, stdout, ln)
	}()

	baseURL := "http://" + ln.Addr().String()
	if werr := testutil.WaitForReady(ctx, t, 10*time.Second, baseURL+"/healthz"); werr != nil {
		t.Fatalf("error waiting for server to be ready: %v", werr)
	}

	t.Cleanup(func() {
		stop()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("server exited with error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server timed out during shutdown")
		}
	})

	return ctx, testServer{BaseURL: baseURL, DBURI: dbURI}
}
