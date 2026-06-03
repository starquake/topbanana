package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/session"
)

// TestRegister_HardGate_NoSessionUntilVerified pins #574 end-to-end: a
// successful registration renders the confirmation page with 200 and no
// live session cookie, commits a verify-token row, and leaves the
// account unable to reach an authenticated route until the email is
// verified and the account logs in.
func TestRegister_HardGate_NoSessionUntilVerified(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// BASE_URL must resolve or SendVerifyEmail short-circuits before
		// the token row commits, breaking the token-count assertion.
		"BASE_URL": "https://topbanana.example",
	})
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	const (
		displayName = "hardgate"
		password    = "hard-gate-pass-123"
	)

	client := authClient(t)
	csrf := fetchCSRFToken(ctx, t, client, srv.BaseURL+"/register")
	resp := postRegister(ctx, t, client, srv.BaseURL, csrf, displayName, password)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck // cleanup.
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	// 1. Register renders the confirmation page (200, not 303).
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("register status = %d, want %d (body=%.300q)", got, want, body)
	}
	if got, want := string(body), "Verify your email"; !strings.Contains(got, want) {
		t.Errorf("register body missing confirmation headline %q; body=%.300q", want, got)
	}

	// 2. No live session cookie was issued.
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("live session cookie set on register: %+v", c)
		}
	}

	// 3. A verify-token row was committed for the new account.
	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if player.EmailVerifiedAt != nil {
		t.Fatalf("EmailVerifiedAt = %v, want nil right after register", player.EmailVerifiedAt)
	}
	waitForVerifyTokenRow(ctx, t, dbConn, player.ID)

	// 4. The registering client cannot reach an authenticated route: its
	// jar carries no session (register cleared it), so /profile bounces
	// to /login.
	gated := getWith(ctx, t, client, srv.BaseURL+"/profile")
	gated.Body.Close() //nolint:errcheck // cleanup.
	if got, want := gated.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("pre-verify /profile status = %d, want %d", got, want)
	}
	if got := gated.Header.Get("Location"); !strings.HasPrefix(got, "/login") {
		t.Errorf("pre-verify /profile Location = %q, want /login...", got)
	}

	// 5. Verify the email, log in, and confirm the account now reaches the
	// authenticated route.
	verifyPlayerEmail(ctx, t, srv.DBURI, displayName)
	loginClient := authClient(t)
	loc := loginForRedirect(ctx, t, loginClient, srv.BaseURL, displayName, password)
	if got, want := loc, "/admin/quizzes"; got != want {
		t.Errorf("post-verify login Location = %q, want %q (first registrant is admin)", got, want)
	}

	after := getWith(ctx, t, loginClient, srv.BaseURL+"/profile")
	defer after.Body.Close() //nolint:errcheck // cleanup.
	if got, want := after.StatusCode, http.StatusOK; got != want {
		t.Errorf("post-verify /profile status = %d, want %d", got, want)
	}
}
