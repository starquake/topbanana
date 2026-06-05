package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestJoinShell_ServesCloakedShell pins the MP-4 (#681) player join surface:
// both the bare /join enter-code entry and the /join/{code} QR deep-link
// target render the same join.html shell bound to the joinApp Alpine
// component, carrying x-cloak so the deferred-Alpine window does not flash
// the wrong phase before init() resolves which one to paint. Alpine strips
// x-cloak at runtime, so the served HTML is the only place the guard is
// observable - hence an HTTP-layer test rather than e2e.
func TestJoinShell_ServesCloakedShell(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, url := range []string{srv.BaseURL + "/join", srv.BaseURL + "/join/ABC234"} {
		assertJoinShell(ctx, t, url)
	}
}

func assertJoinShell(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`x-data="joinApp" x-cloak`,
		`/client/js/join.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q", url, want)
		}
	}
}
