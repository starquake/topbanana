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

// TestWriter is an io.Writer that forwards writes to tb.Log.
// It is thread-safe and ensures logs are captured by the test runner.
type TestWriter struct {
	tb testing.TB
	mu sync.Mutex
}

// NewTestWriter creates a new TestWriter that forwards writes to tb.Log.
func NewTestWriter(tb testing.TB) *TestWriter {
	tb.Helper()

	return &TestWriter{tb: tb}
}

// Write forwards writes to tb.Log.
func (w *TestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// We trim the trailing newline because tb.Log adds one automatically.
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
