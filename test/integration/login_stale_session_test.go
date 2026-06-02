//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/starquake/topbanana/internal/session"
)

// TestLogin_StaleSessionVersionRendersForm pins #615: a validly-signed
// session cookie whose session_version is behind the row's (the state a
// password reset / "sign out other sessions" leaves on a still-open
// browser) must render the login form, not 303 away.
//
// The gating middleware already treats such a cookie as logged-out and
// bounces gated pages to /login?next=...; if GET /login also saw it as
// signed in it would 303 straight back, forming the gate->login->gate
// loop the visitor cannot escape by logging in. So the load-bearing
// assertion is that GET /login returns 200 (the form), not a redirect.
func TestLogin_StaleSessionVersionRendersForm(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	const (
		displayName = "stale-version"
		password    = "correct-battery-stale-13"
	)
	client := authClient(t)
	registerForPending(ctx, t, client, srv.BaseURL, displayName, password)
	verifyPlayerEmail(ctx, t, srv.DBURI, displayName)
	mintStaleSessionCookie(ctx, t, client, srv.BaseURL, srv.DBURI, displayName)

	resp := doRequest(ctx, t, client, http.MethodGet, srv.BaseURL+"/login", nil)
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET /login with a stale-session_version cookie status = %d, want %d "+
			"(the login form, not a redirect that loops with the gate)", got, want)
	}
}

// mintStaleSessionCookie installs a validly-signed session cookie for
// displayName whose session_version is one ahead of the stored row, so
// loadSessionPlayer sees the version mismatch a post-reset cookie would.
// Relies on startServer's default SESSION_KEY (testSessionKey).
func mintStaleSessionCookie(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, dbURI, displayName string,
) {
	t.Helper()

	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("mintStaleSessionCookie GetPlayerByDisplayName err = %v, want nil", err)
	}

	rec := httptest.NewRecorder()
	session.New([]byte(testSessionKey), false).Set(rec, player.ID, player.SessionVersion+1)
	cookie := rec.Result().Cookies()[0]

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse err = %v, want nil", err)
	}
	client.Jar.SetCookies(parsed, []*http.Cookie{cookie})
}
