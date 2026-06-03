package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestClientShell_RootCarriesXCloak pins #605: the player SPA shell hides
// its root until Alpine boots. Alpine removes the x-cloak attribute at
// runtime, so a browser DOM check can't see it - the served HTML is the
// only place the guard is observable, which is why this lives at the HTTP
// layer rather than in e2e.
func TestClientShell_RootCarriesXCloak(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	// Both shell entry points render the same static index.html, so a
	// regression in the template surfaces on either path.
	assertRootCloaked(ctx, t, srv.BaseURL+"/client/")
	assertRootCloaked(ctx, t, srv.BaseURL+"/play/does-not-exist-99999")
}

func assertRootCloaked(ctx context.Context, t *testing.T, url string) {
	t.Helper()

	resp := httpGet(ctx, t, http.DefaultClient, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("%s status = %d, want %d", url, got, want)
	}
	body := readAllString(t, resp.Body)
	if got, want := body, `x-data="gameApp" x-cloak`; !strings.Contains(got, want) {
		t.Errorf(
			"%s body missing %q - the SPA root must carry x-cloak so the stacked x-show panels stay hidden until Alpine boots (#605)",
			url,
			want,
		)
	}
}
