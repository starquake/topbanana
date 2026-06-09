package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestStandingsBarsPartial_SharedAcrossLiveBlocks pins #754: the standings
// bar-graph rows are a single {{define "standings-bars"}} partial
// (partials/standings_bars.html) reused by the round_results, intermission
// (#836), and finished blocks of the live player shell (join.html). Serving the
// shell exercises the render path that parses the partial; a missing, renamed,
// or non-embedded partial fails the parse and returns 500. The partial also
// depends on the static/* embed recursing into the partials subdirectory, which
// this guards in production builds.
func TestStandingsBarsPartial_SharedAcrossLiveBlocks(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+"/join")
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("/join status = %d, want %d", got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`data-testid="standings-bars"`,
		`x-for="bar in standingsBars"`,
		`data-standings-row`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/join body missing %q - the shared standings-bars partial did not expand (#754)", want)
		}
	}
	// Since #773 the standings <ul> is a single container kept mounted across
	// the round_results, intermission (#836), and finished phases via x-show
	// (rather than re-included per phase under x-if), so its list marker appears
	// exactly once in a shell render.
	if got, want := strings.Count(body, `data-testid="standings-bars"`), 1; got != want {
		t.Errorf("/join standings-bars count = %d, want %d (single x-show container, #773)", got, want)
	}
}

// TestBrandMarkPartial_SharedAcrossShells pins #754: the banana wordmark is a
// single {{define "brand-mark"}} partial (partials/brand_mark.html) reused by
// the solo shell (index.html) and the live player shell (join.html). Serving
// both shells exercises the render path that parses the partial.
func TestBrandMarkPartial_SharedAcrossShells(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, url := range []string{srv.BaseURL + "/client/", srv.BaseURL + "/join"} {
		assertBrandMarkPartial(ctx, t, url)
	}
}

func assertBrandMarkPartial(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`aria-label="Top Banana!"`,
		`Top<span class="text-accent">Banana</span>!`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q - the shared brand-mark partial did not expand (#754)", url, want)
		}
	}
}

// TestPlayerHeader_SharedAcrossShells pins #844: the solo shell (index.html)
// and the live player shell (join.html) carry one consistent header - the
// brand mark plus the signed-in account control. The control gates on
// isAuthenticated() (an Alpine expression Alpine evaluates client-side), so
// the inert <template x-if> markup ships in the served HTML for both shells
// regardless of viewer state; this asserts the markup is present and identical
// across the two, which is the unification the ticket is after. The
// browser-side visibility (anonymous never sees it; a signed-in player does
// and the link routes to /profile) is exercised in e2e (pregame-nav.spec.ts).
func TestPlayerHeader_SharedAcrossShells(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, url := range []string{srv.BaseURL + "/client/", srv.BaseURL + "/join"} {
		assertPlayerHeaderAccountControl(ctx, t, url)
	}
}

func assertPlayerHeaderAccountControl(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`x-if="isAuthenticated()"`,
		`Signed in as`,
		`data-testid="account-profile-link"`,
		`href="/profile"`,
		`x-text="player ? player.displayName : ''"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q - the shared player header account control is absent (#844)", url, want)
		}
	}
}
