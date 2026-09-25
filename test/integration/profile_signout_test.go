package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestProfile_SignOutOtherDevices pins #1360: POST /profile/sign-out-other-devices
// invalidates every other copy of the account's session cookie while the
// browser that pressed the button stays signed in. It needs no password, so a
// Google-only account can use it too.
func TestProfile_SignOutOtherDevices(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	t.Run("password account", func(t *testing.T) {
		t.Parallel()

		current := authClient(t)
		registerVerifyAndSignIn(ctx, t, current, srv.BaseURL, srv.DBURI, "signout-pw", "correct-battery-13")
		other := freshClientSharingSession(t, current, srv.BaseURL)

		assertSignOutOtherDevices(ctx, t, current, other, srv.BaseURL)
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

		assertSignOutOtherDevices(ctx, t, current, other, srv.BaseURL)
	})
}

// assertSignOutOtherDevices presses the button from current and checks that
// current keeps its session while other is bounced to /login.
func assertSignOutOtherDevices(ctx context.Context, t *testing.T, current, other *http.Client, baseURL string) {
	t.Helper()

	if got, want := profileGetStatus(ctx, t, other, baseURL), http.StatusOK; got != want {
		t.Fatalf("other browser profile status before = %d, want %d", got, want)
	}

	priming := profileGET(ctx, t, current, baseURL)
	if got, want := priming.body, `action="/profile/sign-out-other-devices"`; !strings.Contains(got, want) {
		t.Errorf("profile body missing %q", want)
	}
	resp := postSignOutOtherDevices(ctx, t, current, baseURL, url.Values{"csrf_token": {priming.csrf}})

	if got, want := resp.status, http.StatusSeeOther; got != want {
		t.Fatalf("sign-out-other-devices status = %d, want %d", got, want)
	}
	if got, want := resp.location, "/profile"; got != want {
		t.Errorf("sign-out-other-devices Location = %q, want %q", got, want)
	}
	if got, want := resp.sessionCookie, true; got != want {
		t.Errorf("sign-out-other-devices re-issued session cookie = %v, want %v", got, want)
	}

	notice := "Signed out of every other browser and device."
	if got, want := profileGET(ctx, t, current, baseURL).body, notice; !strings.Contains(got, want) {
		t.Errorf("profile after redirect missing %q", want)
	}
	if got, want := profileGET(ctx, t, current, baseURL).body, notice; strings.Contains(got, want) {
		t.Errorf("profile reload still shows %q, want the flash consumed", want)
	}

	if got, want := profileGetStatus(ctx, t, current, baseURL), http.StatusOK; got != want {
		t.Errorf("current browser profile status after = %d, want %d (stays signed in)", got, want)
	}
	if got, want := profileGetStatus(ctx, t, other, baseURL), http.StatusSeeOther; got != want {
		t.Errorf("other browser profile status after = %d, want %d (redirect to login)", got, want)
	}
}

// TestProfile_SignOutOtherDevicesKeepsAdminNext pins that the redirect back to
// /profile carries a validated admin return target.
func TestProfile_SignOutOtherDevicesKeepsAdminNext(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})
	client := authClient(t)
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "signout-next", "correct-battery-14")
	priming := profileGET(ctx, t, client, srv.BaseURL)

	resp := postSignOutOtherDevices(ctx, t, client, srv.BaseURL, url.Values{
		"csrf_token": {priming.csrf},
		"next":       {"/admin/quizzes"},
	})
	if got, want := resp.status, http.StatusSeeOther; got != want {
		t.Fatalf("sign-out-other-devices status = %d, want %d", got, want)
	}
	if got, want := resp.location, "/profile?next=%2Fadmin%2Fquizzes"; got != want {
		t.Errorf("sign-out-other-devices Location = %q, want %q", got, want)
	}
}

// signOutResult is the part of the unfollowed sign-out response the tests check.
type signOutResult struct {
	status        int
	location      string
	sessionCookie bool
}

// postSignOutOtherDevices submits form to the sign-out-other-devices endpoint
// without following the redirect.
func postSignOutOtherDevices(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, form url.Values,
) signOutResult {
	t.Helper()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/profile/sign-out-other-devices", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	readAllClose(t, resp)

	return signOutResult{
		status:        resp.StatusCode,
		location:      resp.Header.Get("Location"),
		sessionCookie: hasSessionCookie(resp),
	}
}
