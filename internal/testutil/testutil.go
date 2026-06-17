// Package testutil contains utilities for testing.
package testutil

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"testing"
	"time"
)

const (
	clientTimeout             = 5 * time.Second
	waitForReadyRetryInterval = 250 * time.Millisecond
)

// SignalCtx returns a context that is canceled when the test is interrupted
// (e.g., via the Stop button in an IDE).
func SignalCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt)
	t.Cleanup(stop)

	return ctx, stop
}

// TestWriter is an [io.Writer] that forwards writes to tb.Log while the test is
// running. It is safe for concurrent use and stops forwarding once the test is
// tearing down (see [TestWriter.Disable], also wired to a t.Cleanup): calling
// tb.Log from a goroutine after the test has begun completing races the testing
// framework's own teardown. The canonical trigger is an integration server
// still logging an in-flight request while the test it belongs to is finishing
// (#1008); after Disable those late writes are dropped rather than forwarded.
type TestWriter struct {
	tb   testing.TB
	mu   sync.Mutex
	done bool
}

// NewTestWriter creates a TestWriter that forwards writes to tb.Log. It
// registers a t.Cleanup that disables forwarding, so a stray late write never
// reaches tb.Log after this test's cleanups run. A caller that owns a
// background logger (e.g. a test server) should additionally call Disable at
// the start of its own shutdown, before the writer's source is stopped.
func NewTestWriter(tb testing.TB) *TestWriter {
	tb.Helper()

	w := &TestWriter{tb: tb}
	tb.Cleanup(w.Disable)

	return w
}

// Disable stops the writer forwarding to tb.Log; subsequent writes are dropped.
// It is idempotent and safe to call concurrently with Write.
func (w *TestWriter) Disable() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.done = true
}

// Write forwards writes to tb.Log until the writer is disabled, after which it
// drops them: tb.Log is not safe to call once the test is completing.
func (w *TestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done {
		return len(p), nil
	}

	w.tb.Logf("%s", string(p))

	return len(p), nil
}

// WaitForReady calls the specified endpoint until it gets a 200
// response or until the context is canceled or the timeout is
// reached.
func WaitForReady(
	ctx context.Context,
	t *testing.T,
	timeout time.Duration,
	endpoint string,
) error {
	t.Helper()

	client := http.Client{
		Timeout: clientTimeout,
	}
	ticker := time.NewTicker(waitForReadyRetryInterval)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for endpoint: %w", timeoutCtx.Err())
		default:
		}

		req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			closeErr := resp.Body.Close()
			if closeErr != nil {
				return fmt.Errorf("failed to close response body: %w", closeErr)
			}
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for endpoint: %w", timeoutCtx.Err())
		case <-ticker.C:
		}
	}
}
