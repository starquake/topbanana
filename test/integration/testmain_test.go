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

// seededAdminID is the id of the admin row inserted by migration
// 20260111110308_add_admin_player.sql. Integration tests that seed
// quizzes directly through the store attribute them to this admin so
// the NOT NULL created_by_player_id column (migration 20260520200000,
// #281) is satisfied.
const seededAdminID int64 = 1

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
//
// runOpts are forwarded to [app.Run] for values that have no env-var
// hook: the HTTP server's WriteTimeout and the SSE handlers' heartbeat
// intervals, which the heartbeat regression tests shrink so the
// assertion runs inside a sub-second window.
func startServer(
	t *testing.T, extraEnv map[string]string, runOpts ...app.Option,
) (context.Context, testServer) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real server")
	}

	ctx, stop := testutil.SignalCtx(t)
	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		// APP_ENV=development keeps cookies non-Secure so the
		// integration tests' http.Client (no TLS) gets the cookies
		// the handlers set. Tests that need to exercise Secure-cookie
		// behaviour can override APP_ENV via extraEnv.
		env := map[string]string{
			"APP_ENV": "development",
			"HOST":    "localhost",
			"PORT":    "0",
			"DB_URI":  dbURI,
			// Fixed signing key so tests can mint a matching session cookie
			// via mintSessionCookie (auth_redirect_test.go). The #574 hard
			// gate stopped register from handing out a session, so tests
			// that need a signed-in-but-unverified client forge the cookie
			// directly. Overridable via extraEnv.
			"SESSION_KEY": testSessionKey,
			// Quiet the MP-5 session runner by default. Its 250ms beat
			// polls the DB (ListLiveSessionIDs) in every test server, and
			// ~100 parallel servers each polling under -race + coverage adds
			// enough background load to widen the #608 readiness flake. The
			// vast majority of integration tests never exercise the runner,
			// so park its beat; the runner tests opt back in via extraEnv
			// (SESSION_RUNNER_BEAT=30ms), which maps.Copy layers on top.
			"SESSION_RUNNER_BEAT": "1h",
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
		errCh <- app.Run(ctx, getenv, stdout, ln, runOpts...)
	}()

	// Coverage instrumentation (make test-coverage, the CI build job) plus
	// -race slows server startup enough that a batch of parallel boots -
	// each running every migration on a fresh DB - can blow past a tight
	// readiness budget all at once (#608). Widen the budget only under
	// coverage; a plain `make test-integration` keeps the short budget so
	// a genuine startup hang still fails fast.
	readyTimeout := 10 * time.Second
	if testing.CoverMode() != "" {
		readyTimeout = 60 * time.Second
	}
	baseURL := "http://" + ln.Addr().String()
	if werr := testutil.WaitForReady(ctx, t, readyTimeout, baseURL+"/healthz"); werr != nil {
		t.Fatalf("error waiting for server to be ready: %v", werr)
	}

	t.Cleanup(func() {
		// Stop forwarding the server's request logs to t.Log before draining
		// it: an in-flight request that logs during/after shutdown would
		// otherwise call t.Log as the test completes and race the testing
		// framework's own teardown (#1008).
		stdout.Disable()
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
