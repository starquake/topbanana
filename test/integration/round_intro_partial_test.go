package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestRoundIntroPartial_SharedAcrossShells pins #748: the round-intro title
// and summary markup is a single {{define "round-intro-card"}} partial
// (partials/round_intro.html) reused by both the solo shell (index.html) and
// the live player shell (join.html). Serving the shells exercises the render
// path that parses the partial; a missing, renamed, or non-embedded partial
// fails the parse and returns 500. The partial also depends on the static/*
// embed recursing into the partials subdirectory, which this guards in
// production builds.
func TestRoundIntroPartial_SharedAcrossShells(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	for _, url := range []string{srv.BaseURL + "/client/", srv.BaseURL + "/join"} {
		assertRoundIntroPartial(ctx, t, url)
	}
}

func assertRoundIntroPartial(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	for _, want := range []string{
		`data-testid="round-title"`,
		`x-text="roundTitle()"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s body missing %q - the shared round-intro partial did not expand (#748)", url, want)
		}
	}
}
