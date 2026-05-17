//go:build integration

package integration_test

import (
	"testing"
)

func TestHeath_Integration(t *testing.T) {
	t.Parallel()

	// startServer waits for /healthz to return 200 before returning, so a
	// successful call here is by itself a passing health check. The
	// cleanup it registers also asserts the server shuts down cleanly.
	startServer(t, nil)
}
