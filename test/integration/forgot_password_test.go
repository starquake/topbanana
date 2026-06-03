package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestForgotPassword_GETRendersForm pins that an anonymous visitor
// reaches the forgot-password form and sees the identifier input + a
// CSRF token.
func TestForgotPassword_GETRendersForm(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	resp := getWith(ctx, t, authClient(t), srv.BaseURL+"/forgot-password")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	for _, want := range []string{`name="identifier"`, `name="csrf_token"`} {
		if got := string(body); !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestForgotPassword_POSTAlwaysFlashesGenericSuccess pins the
// account-existence-opaque contract: identical response for an
// unknown identifier, a real displayName, and a real email. Each
// subtest gets its own server (and so its own per-IP rate-limit
// bucket) - sharing one server would have the first POST stamp the
// limiter and the next two POSTs see the "slow down" flash instead
// of the generic flash, defeating the assertion.
func TestForgotPassword_POSTAlwaysFlashesGenericSuccess(t *testing.T) {
	t.Parallel()

	for _, ident := range []string{"forgot-ghost", "forgot-real", "forgot-real@example.test"} {
		t.Run(ident, func(t *testing.T) {
			t.Parallel()

			ctx, srv := startServer(t, map[string]string{
				"REGISTRATION_ENABLED": "true",
			})
			dbConn, stores := openStores(t, srv.DBURI)
			defer dbConn.Close() //nolint:errcheck // cleanup.

			if _, err := stores.Players.CreatePlayer(
				ctx, "forgot-real", "forgot-real@example.test", "h", "player",
			); err != nil {
				t.Fatalf("CreatePlayer err = %v, want nil", err)
			}

			client := authClient(t)
			resp := postForgot(ctx, t, client, srv.BaseURL, ident)
			defer resp.Body.Close() //nolint:errcheck // cleanup.
			if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := resp.Header.Get("Location"), "/forgot-password"; got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}
			follow := getWith(ctx, t, client, srv.BaseURL+"/forgot-password")
			defer follow.Body.Close() //nolint:errcheck // cleanup.
			body, err := io.ReadAll(follow.Body)
			if err != nil {
				t.Fatalf("ReadAll err = %v, want nil", err)
			}
			// html/template escapes the apostrophe in "we've", so match a
			// substring that survives HTML escaping.
			if got, want := string(body), "If an account matches"; !strings.Contains(got, want) {
				t.Errorf("body missing generic flash; body=%q", got)
			}
		})
	}
}

// TestForgotPassword_RateLimited pins that two POSTs from one IP in
// quick succession get rate-limited on the second, with Retry-After
// surfaced as a non-zero integer.
func TestForgotPassword_RateLimited(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	first := postForgot(ctx, t, client, srv.BaseURL, "anyone")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := postForgot(ctx, t, client, srv.BaseURL, "anyone")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST")
	}
}

// postForgot fetches the CSRF token from /forgot-password, then
// POSTs the form with the given identifier. Returns the raw response
// so the caller can assert status / headers.
func postForgot(ctx context.Context, t *testing.T, client *http.Client, baseURL, identifier string) *http.Response {
	t.Helper()
	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/forgot-password")

	form := url.Values{}
	form.Add("identifier", identifier)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/forgot-password", strings.NewReader(form.Encode()),
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
