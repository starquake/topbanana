// Package testutil contains utilities for testing.
package testutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"
)

const (
	serverAddrTimeout         = 10 * time.Second
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

// ServerAddress returns the address of the server that is listening on, by checking the log of the
// specified pipe reader.
func ServerAddress(t *testing.T, pr *io.PipeReader) string {
	t.Helper()

	serverAddrCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("Server log: %s", line)
			if strings.Contains(line, "listening on") {
				parts := strings.Split(line, "addr=")
				if len(parts) > 1 {
					addr := strings.Split(parts[1], " ")[0]
					addr = strings.Trim(addr, "\"")
					select {
					case serverAddrCh <- addr:
					default:
					}
				}
			}
		}
	}()

	var serverAddr string
	select {
	case serverAddr = <-serverAddrCh:
	case <-time.After(serverAddrTimeout):
		t.Fatal("timed out waiting for server address in logs")
	}

	return serverAddr
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
