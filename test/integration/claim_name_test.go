//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// TestClaimName_NonAnonymousReturnsAlreadyClaimed pins the #289 fix:
// PATCH /api/players/me on a credentialled player must return 409 with
// a JSON body of {"code":"already_claimed","message":"..."}. The
// player client (PlayerService.claimName) branches on this code to
// dismiss the modal instead of showing "name is taken", which was the
// pre-fix UX dead-end for the seed admin.
func TestClaimName_NonAnonymousReturnsAlreadyClaimed(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Register a password-bearing player. The /register handler sets
	// password_hash + username_claimed=1, so this row is the
	// "non-anonymous" case the PATCH must reject.
	registerPlayer(ctx, t, client, baseURL, "claim-resident", "claim-resident-pass-123")

	body, status := patchPlayerUsernameWithBody(ctx, t, client, baseURL, "different-name")
	if got, want := status, http.StatusConflict; got != want {
		t.Fatalf("PATCH status = %d, want %d (body=%q)", got, want, body)
	}

	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body err = %v (raw=%q)", err, body)
	}
	if got, want := payload.Code, "already_claimed"; got != want {
		t.Errorf("body.code = %q, want %q (raw=%q)", got, want, body)
	}
	if payload.Message == "" {
		t.Error("body.message is empty, want a non-empty human-readable string")
	}
}

// registerPlayer posts /register through the supplied client so the
// resulting session cookie lands on its jar. Mirrors the existing
// registerAdminViaHTTP helper but with caller-supplied credentials so
// the test can pick an account that won't collide with sibling tests.
func registerPlayer(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username, password string,
) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")

	form := url.Values{}
	form.Add("username", username)
	form.Add("password", password)
	form.Add("csrf_token", token)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/register", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
}

// patchPlayerUsernameWithBody is patchPlayerUsername (in anonymous_test.go)
// but also returns the response body so the caller can assert on the
// structured error JSON introduced for #289.
func patchPlayerUsernameWithBody(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username string,
) ([]byte, int) {
	t.Helper()

	body := fmt.Sprintf(`{"username": %q}`, username)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPatch, baseURL+"/api/players/me", strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return raw, resp.StatusCode
}
