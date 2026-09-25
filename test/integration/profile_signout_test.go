package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestProfile_SignOutEverywhere pins #1360: POST /profile/sign-out-everywhere
// invalidates every other copy of the account's session cookie while the
// browser that pressed the button stays signed in. It needs no password, so a
// Google-only account can use it too.
func TestProfile_SignOutEverywhere(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	t.Run("password account", func(t *testing.T) {
		t.Parallel()

		current := authClient(t)
		registerVerifyAndSignIn(ctx, t, current, srv.BaseURL, srv.DBURI, "signout-pw", "correct-battery-13")
		other := freshClientSharingSession(t, current, srv.BaseURL)

		assertSignOutEverywhere(ctx, t, current, other, srv.BaseURL)
	})

	t.Run("Google-only account", func(t *testing.T) {
		t.Parallel()

		dbConn, stores := openStores(t, srv.DBURI)
		defer dbConn.Close() //nolint:errcheck // cleanup.
		if _, err := stores.OAuth.CreatePlayerFromOAuth(
			ctx,
			"signout-google",
			"signout-google@example.test",
		); err != nil {
			t.Fatalf("CreatePlayerFromOAuth err = %v, want nil", err)
		}

		current := authClient(t)
		mintSessionCookie(ctx, t, current, srv.BaseURL, srv.DBURI, "signout-google")
		other := authClient(t)
		mintSessionCookie(ctx, t, other, srv.BaseURL, srv.DBURI, "signout-google")

		assertSignOutEverywhere(ctx, t, current, other, srv.BaseURL)
	})
}

// assertSignOutEverywhere presses the button from current and checks that
// current keeps its session while other is bounced to /login.
func assertSignOutEverywhere(ctx context.Context, t *testing.T, current, other *http.Client, baseURL string) {
	t.Helper()

	if got, want := profileGetStatus(ctx, t, other, baseURL), http.StatusOK; got != want {
		t.Fatalf("other browser profile status before = %d, want %d", got, want)
	}

	priming := profileGET(ctx, t, current, baseURL)
	if got, want := priming.body, `action="/profile/sign-out-everywhere"`; !strings.Contains(got, want) {
		t.Errorf("profile body missing %q", want)
	}
	form := url.Values{"csrf_token": {priming.csrf}}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/sign-out-everywhere", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := current.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body := readAllClose(t, resp)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("sign-out-everywhere status = %d, want %d", got, want)
	}
	if got, want := body, "Signed out of every other browser and device."; !strings.Contains(got, want) {
		t.Errorf("sign-out-everywhere body missing %q", want)
	}
	if got, want := hasSessionCookie(resp), true; got != want {
		t.Errorf("sign-out-everywhere re-issued session cookie = %v, want %v", got, want)
	}

	if got, want := profileGetStatus(ctx, t, current, baseURL), http.StatusOK; got != want {
		t.Errorf("current browser profile status after = %d, want %d (stays signed in)", got, want)
	}
	if got, want := profileGetStatus(ctx, t, other, baseURL), http.StatusSeeOther; got != want {
		t.Errorf("other browser profile status after = %d, want %d (redirect to login)", got, want)
	}
}
