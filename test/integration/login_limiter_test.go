//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// TestLogin_RateLimited pins #494: two POSTs to /login from the same
// IP in quick succession get exactly one served as 429 with a
// Retry-After header. Drives the real handler chain so the route
// wiring + CSRF + limiter all participate.
//
// Fires the POSTs concurrently rather than sequentially because the
// dummy-hash bcrypt path on a slow CI runner can exceed the 3s
// cooldown window between two sequential requests, letting the
// "second" POST out from under the limiter. Concurrent fire-and-race
// keeps the test independent of bcrypt cost.
func TestLogin_RateLimited(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client1 := authClient(t)
	client2 := authClient(t)
	csrf1 := fetchCSRFToken(ctx, t, client1, srv.BaseURL+"/login")
	csrf2 := fetchCSRFToken(ctx, t, client2, srv.BaseURL+"/login")

	var wg sync.WaitGroup
	resps := make([]*http.Response, 2)
	pairs := []struct {
		client *http.Client
		csrf   string
	}{{client1, csrf1}, {client2, csrf2}}
	for i, pair := range pairs {
		wg.Go(func() {
			//nolint:bodyclose // closed below after the rate-limited / normal split is settled.
			resps[i] = postLoginWithToken(ctx, t, pair.client, srv.BaseURL, pair.csrf, "ghost", "whatever-password")
		})
	}
	wg.Wait()

	var limited, normal *http.Response
	for _, r := range resps {
		switch r.StatusCode {
		case http.StatusTooManyRequests:
			limited = r
		case http.StatusUnauthorized:
			normal = r
		default:
			// Unexpected; the assertion below surfaces it with both codes in the message.
		}
	}
	if normal == nil || limited == nil {
		for _, r := range resps {
			r.Body.Close() //nolint:errcheck // cleanup.
		}
		t.Fatalf("expected one 401 + one 429, got %d and %d",
			resps[0].StatusCode, resps[1].StatusCode)
	}
	defer normal.Body.Close()  //nolint:errcheck // cleanup.
	defer limited.Body.Close() //nolint:errcheck // cleanup.

	if got := limited.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited POST")
	}
	body, err := io.ReadAll(limited.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Too many attempts"; !strings.Contains(got, want) {
		t.Errorf("body missing rate-limit banner; body=%.300q", got)
	}
}

// postLoginWithToken POSTs /login with the supplied credentials and a
// pre-fetched CSRF token, returning the raw response. The token is
// passed in (rather than fetched here) so two concurrent POSTs can
// share their /login GET round-trips and reach the limiter at nearly
// the same time.
func postLoginWithToken(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, csrfToken, username, password string,
) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Add("username", username)
	form.Add("password", password)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}
