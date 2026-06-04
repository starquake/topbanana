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

// TestClientShell_PreloadsFonts pins #691: the player SPA shell must
// preload the above-the-fold font subsets so the browser fetches them
// before parsing app.css. Without the preload the first load paints the
// fallback font and the custom face only appears on a refresh (the
// service worker has it cached by then). The extended-latin subset is
// deliberately not preloaded.
func TestClientShell_PreloadsFonts(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	resp := httpGet(ctx, t, http.DefaultClient, srv.BaseURL+"/client/")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := readAllString(t, resp.Body)

	for _, want := range []string{
		`<link rel="preload" href="/assets/fonts/inter-latin.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel="preload" href="/assets/fonts/orbitron-latin.woff2" as="font" type="font/woff2" crossorigin>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("client shell missing font preload %q (#691)", want)
		}
	}
	if banned := `rel="preload" href="/assets/fonts/inter-latin-ext.woff2"`; strings.Contains(body, banned) {
		t.Error("client shell preloads the extended-latin subset, which should not be preloaded (#691)")
	}
}
