package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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

	// Register a password-bearing player and sign them in. The /register
	// handler sets password_hash + displayName_claimed=1, so this row is
	// the "non-anonymous" case the PATCH must reject. The hard gate
	// (#574) means a session only arrives after verify + login.
	registerVerifyAndSignIn(ctx, t, client, baseURL, srv.DBURI, "claim-resident", "claim-resident-pass-123")

	body, status := patchPlayerDisplayNameWithBody(ctx, t, client, baseURL, "different-name")
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

// TestClaimName_TooLongRejected pins that the claim-name endpoint rejects an
// over-long display name with a 400 and the display_name_too_long code.
func TestClaimName_TooLongRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})
	baseURL := srv.BaseURL

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{Jar: jar}

	// GET /api/players/me mints an anonymous player + session cookie, so the
	// follow-up PATCH lands on a claimable row.
	_ = fetchPlayerMe(ctx, t, client, baseURL)

	// 51 runes is one over the MaxDisplayNameLength cap.
	tooLong := strings.Repeat("a", 51)
	body, status := patchPlayerDisplayNameWithBody(ctx, t, client, baseURL, tooLong)
	if got, want := status, http.StatusBadRequest; got != want {
		t.Fatalf("PATCH status = %d, want %d (body=%q)", got, want, body)
	}

	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body err = %v (raw=%q)", err, body)
	}
	if got, want := payload.Code, "display_name_too_long"; got != want {
		t.Errorf("body.code = %q, want %q (raw=%q)", got, want, body)
	}
}

// patchPlayerDisplayNameWithBody is patchPlayerDisplayName (in anonymous_test.go)
// but also returns the response body so the caller can assert on the
// structured error JSON introduced for #289.
func patchPlayerDisplayNameWithBody(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName string,
) ([]byte, int) {
	t.Helper()

	body := fmt.Sprintf(`{"displayName": %q}`, displayName)
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
