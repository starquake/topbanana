//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestHomeFooter_StaleSessionVersionShowsLoggedOut pins #620: the home
// footer's "signed in" check resolves the session through the same
// version-aware path as the gates, so a cookie left version-stale by a
// password reset does not render "Signed in as ..." while every gated
// page treats it as logged out.
func TestHomeFooter_StaleSessionVersionShowsLoggedOut(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	const (
		displayName = "footer-stale"
		password    = "correct-battery-footer-13"
	)
	client := authClient(t)
	registerForPending(ctx, t, client, srv.BaseURL, displayName, password)
	verifyPlayerEmail(ctx, t, srv.DBURI, displayName)
	mintStaleSessionCookie(ctx, t, client, srv.BaseURL, srv.DBURI, displayName)

	resp := doRequest(ctx, t, client, http.MethodGet, srv.BaseURL+"/", nil)
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET / status = %d, want %d", got, want)
	}
	if strings.Contains(string(body), "Signed in as") {
		t.Error(`home footer rendered "Signed in as" for a version-stale cookie; want the logged-out state`)
	}
}
