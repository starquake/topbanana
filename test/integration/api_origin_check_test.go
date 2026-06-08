package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestAPIOriginCheck_RejectsCrossSite pins the #784 same-origin guard on the
// JSON API. A mutating /api/* request whose Sec-Fetch-Site is cross-site, or
// whose Origin does not match the server, is rejected with 403 before the
// handler runs; a same-origin request (matching Sec-Fetch-Site or Origin) and a
// header-less request (a non-browser client) both reach the handler.
func TestAPIOriginCheck_RejectsCrossSite(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "sec-fetch-site cross-site rejected",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    http.StatusForbidden,
		},
		{
			name:    "sec-fetch-site none rejected",
			headers: map[string]string{"Sec-Fetch-Site": "none"},
			want:    http.StatusForbidden,
		},
		{
			name:    "cross-site origin rejected",
			headers: map[string]string{"Origin": "https://evil.example.com"},
			want:    http.StatusForbidden,
		},
		{
			name:    "sec-fetch-site same-origin allowed",
			headers: map[string]string{"Sec-Fetch-Site": "same-origin"},
			want:    http.StatusOK,
		},
		{
			name:    "sec-fetch-site same-site allowed",
			headers: map[string]string{"Sec-Fetch-Site": "same-site"},
			want:    http.StatusOK,
		},
		{
			name:    "matching origin allowed",
			headers: map[string]string{"Origin": baseURL},
			want:    http.StatusOK,
		},
		{
			name:    "no origin headers allowed",
			headers: nil,
			want:    http.StatusOK,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each subtest claims a distinct name on its own anonymous
			// player so the allowed cases succeed (200) instead of
			// colliding on a name another subtest already took (409).
			displayName := fmt.Sprintf("origin-guard-%d", i)
			got := patchDisplayNameWithHeaders(ctx, t, baseURL, displayName, tc.headers)
			if got != tc.want {
				t.Errorf("PATCH /api/players/me status = %d, want %d", got, tc.want)
			}
		})
	}
}

// patchDisplayNameWithHeaders mints a fresh anonymous player (its own cookie
// jar) and issues PATCH /api/players/me with the given displayName and extra
// request headers, returning the response status.
func patchDisplayNameWithHeaders(
	ctx context.Context, t *testing.T, baseURL, displayName string, headers map[string]string,
) int {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{Jar: jar}

	body := fmt.Sprintf(`{"displayName": %q}`, displayName)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPatch, baseURL+"/api/players/me", strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	}()

	return resp.StatusCode
}
