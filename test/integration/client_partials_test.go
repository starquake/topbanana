package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestStandingsBarsPartial_SharedAcrossLiveBlocks pins #754: the standings
// bar-graph rows are a single {{define "standings-bars"}} partial
// (partials/standings_bars.html) reused by both the round_results and finished
// blocks of the live player shell (join.html). Serving the shell exercises the
// render path that parses the partial; a missing, renamed, or non-embedded
// partial fails the parse and returns 500. The partial also depends on the
// static/* embed recursing into the partials subdirectory, which this guards in
// production builds.
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
	// The partial is invoked from both the round_results and finished blocks, so
	// its list marker appears twice in a single shell render.
	if got, want := strings.Count(body, `data-testid="standings-bars"`), 2; got != want {
		t.Errorf("/join standings-bars count = %d, want %d (round_results + finished)", got, want)
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
