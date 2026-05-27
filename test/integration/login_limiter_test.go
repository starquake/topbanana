//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestLogin_RateLimited pins #494: two POSTs to /login from the same
// IP in quick succession get the second served as 429 with a
// Retry-After header. Drives the real handler chain so the route
// wiring + CSRF + limiter all participate.
func TestLogin_RateLimited(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	first := postLogin(ctx, t, client, srv.BaseURL, "ghost", "whatever-password")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := postLogin(ctx, t, client, srv.BaseURL, "ghost", "whatever-password")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusTooManyRequests; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := string(body), "Too many attempts"; !strings.Contains(got, want) {
		t.Errorf("body missing rate-limit banner; body=%.300q", got)
	}
}

// postLogin fetches the CSRF token from /login then POSTs the form
// with the supplied credentials. Returns the raw response so the
// caller can assert status + headers + body.
func postLogin(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username, password string,
) *http.Response {
	t.Helper()
	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/login")

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
