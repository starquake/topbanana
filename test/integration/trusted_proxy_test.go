//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestTrustedProxy_XFFHonouredWhenConfigured pins #463: when
// TRUSTED_PROXY_IPS lists the loopback range, two POSTs from the same
// peer with different X-Forwarded-For headers must land in different
// rate-limit buckets. Without the env var (the default), XFF is
// ignored and the second POST is rate-limited because both share the
// loopback RemoteAddr.
func TestTrustedProxy_XFFHonouredWhenConfigured(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"TRUSTED_PROXY_IPS":    "127.0.0.1/32,::1/128",
	})

	client := authClient(t)
	first := postForgotWithXFF(ctx, t, client, srv.BaseURL, "1.2.3.4")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}
	if got := first.Header.Get("Retry-After"); got != "" {
		t.Errorf("Retry-After header = %q on first POST, want empty", got)
	}

	second := postForgotWithXFF(ctx, t, client, srv.BaseURL, "5.6.7.8")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got != "" {
		t.Errorf("Retry-After header = %q on second POST with distinct XFF, want empty", got)
	}
}

// TestTrustedProxy_XFFIgnoredByDefault pins the fail-secure default:
// without TRUSTED_PROXY_IPS set, an XFF header is ignored and the
// limiter buckets on RemoteAddr, so two POSTs from the same peer
// rate-limit each other regardless of the XFF value they ship.
func TestTrustedProxy_XFFIgnoredByDefault(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	first := postForgotWithXFF(ctx, t, client, srv.BaseURL, "1.2.3.4")
	first.Body.Close() //nolint:errcheck // cleanup.
	if got, want := first.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := postForgotWithXFF(ctx, t, client, srv.BaseURL, "5.6.7.8")
	defer second.Body.Close() //nolint:errcheck // cleanup.
	if got, want := second.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got := second.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After header empty on rate-limited second POST; XFF should be ignored without TRUSTED_PROXY_IPS")
	}
}

func postForgotWithXFF(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, xff string,
) *http.Response {
	t.Helper()
	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/forgot-password")

	form := url.Values{}
	form.Add("identifier", "anyone")
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/forgot-password", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", xff)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}
