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

// TestRegister_EmailCollisionOpaque pins the account-enumeration opacity
// contract (#573): POST /register with an already-registered email must
// return a response identical (status + body) to a fresh-email signup,
// so the form cannot be used to learn which addresses have accounts.
func TestRegister_EmailCollisionOpaque(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	// Register the owner so owner@example.test is taken.
	owner := authClient(t)
	registerForPending(ctx, t, owner, srv.BaseURL, "enum-owner", "correct-battery-13")

	// Fresh email: the baseline the collision must match.
	freshStatus, freshBody := registerRaw(
		ctx, t, authClient(t), srv.BaseURL, "enum-newcomer", "enum-newcomer@example.test", "correct-battery-13",
	)

	// Already-registered email under a different display name.
	collideStatus, collideBody := registerRaw(
		ctx, t, authClient(t), srv.BaseURL, "enum-prober", "enum-owner@example.test", "correct-battery-13",
	)

	if got, want := collideStatus, freshStatus; got != want {
		t.Errorf("collision status = %d, want %d (fresh)", got, want)
	}
	if got, want := collideStatus, http.StatusOK; got != want {
		t.Errorf("collision status = %d, want %d", got, want)
	}

	// The bodies echo the typed email, so normalise that difference out
	// before comparing - everything else must be byte-identical.
	freshNorm := strings.ReplaceAll(freshBody, "enum-newcomer@example.test", "EMAIL")
	collideNorm := strings.ReplaceAll(collideBody, "enum-owner@example.test", "EMAIL")
	if freshNorm != collideNorm {
		t.Errorf("collision body differs from fresh body after email normalisation:\nfresh=%.400q\ncollide=%.400q",
			freshNorm, collideNorm)
	}

	// The byte-identical comparison above is the definitive opacity
	// proof; this guards the specific pre-#573 leak phrasing in case the
	// fresh and collision bodies ever drift together in a way the
	// normalised compare misses.
	if strings.Contains(collideBody, "already registered") {
		t.Errorf("collision body leaked account existence; body=%.400q", collideBody)
	}
}

// registerRaw POSTs /register with an explicit email (decoupled from the
// username, unlike postRegister) and returns the status and body. Used by
// the opacity test which needs two distinct usernames sharing one email.
func registerRaw(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, username, email, password string,
) (int, string) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")

	form := url.Values{}
	form.Add("display_name", username)
	form.Add("email", email)
	form.Add("password", password)
	form.Add("password_confirm", password)
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
	defer resp.Body.Close() //nolint:errcheck // cleanup.

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return resp.StatusCode, string(body)
}
