//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestAdmin_Integration(t *testing.T) {
	t.Parallel()

	var err error

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	defer cleanup()

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":   "localhost",
			"PORT":   "0", // Let the OS choose an available port
			"DB_URI": dbURI,
		}

		return env[key]
	}

	listenConfig := &net.ListenConfig{}
	var ln net.Listener
	ln, err = listenConfig.Listen(ctx, "tcp", net.JoinHostPort(getenv("HOST"), getenv("PORT")))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, stdout, ln)
	}()

	serverAddr := ln.Addr().String()
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("http://%s/healthz", serverAddr))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

	// Shutdown server
	stop()
	select {
	case err = <-errCh:
		// Ignore context.Canceled because we triggered it ourselves via stop()
		if err != nil && !errors.Is(err, context.Background().Err()) && !errors.Is(err, context.Canceled) {
			t.Errorf("server exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("server timed out during shutdown")
	}
}
