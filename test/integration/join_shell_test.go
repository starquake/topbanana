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
		`/client/js/dist/join.js`,
		// #844: the unified player header drops out while a live question is
		// on screen, mirroring the solo client's `gameId && !finished` gate.
		// JoinApp.inActiveQuestion() maps that intent to the live phase model
		// (question + reveal). The served markup must carry the gate so the
		// header rides in flow on every other screen and hides during a
		// question.
		`x-show="!inActiveQuestion()"`,
		// #865: the name phase carries a Sign-in affordance for anonymous
		// joiners, whose href is built with Alpine so it carries the login
		// deep-link return (/login?next=/join/{code}) back to this same room.
		// Both /join and /join/{code} serve this markup.
		`data-testid="join-name-signin"`,
		`'/login?next=' + encodeURIComponent('/join/' + code)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q", url, want)
		}
	}
}
