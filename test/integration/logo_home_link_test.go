//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// wantLogoHomeLink is the anchor that makes the Top Banana brand mark a
// link to the home page. Every auth surface should render it so clicking
// the logo always returns home (#609).
const wantLogoHomeLink = `<a href="/" aria-label="Top Banana!"`

// TestAuthPagesLogoLinksHome pins #609: the logo on the public auth
// pages links to home, matching login/register. The dead-token paths
// deliberately hit the invalid/expired variants, which carry the same
// brand mark. Covers the pages reachable with a plain GET; the
// register-pending page is exercised separately below since it needs a
// POST.
func TestAuthPagesLogoLinksHome(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	paths := []string{
		"/login",
		"/register",
		"/forgot-password",
		"/verify-email/request",
		"/verify-email?token=deadbeef",
		"/reset-password?token=deadbeef",
		"/accept-invite?token=deadbeef",
	}

	for _, path := range paths {
		resp := doRequest(ctx, t, authClient(t), http.MethodGet, srv.BaseURL+path, nil)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck // cleanup.
		if err != nil {
			t.Fatalf("ReadAll %s err = %v, want nil", path, err)
		}
		if got := string(body); !strings.Contains(got, wantLogoHomeLink) {
			t.Errorf("GET %s body missing home-linked logo %q", path, wantLogoHomeLink)
		}
	}
}

// TestRegisterPendingLogoLinksHome covers the post-register confirmation
// page (#574 hard gate), which renders register_pending.gohtml and is
// only reachable via a successful POST /register.
func TestRegisterPendingLogoLinksHome(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})

	client := authClient(t)
	token := fetchCSRFToken(ctx, t, client, srv.BaseURL+"/register")
	resp := postRegister(ctx, t, client, srv.BaseURL, token, "logo-pending", "correct-battery-13")
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck // cleanup.
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
	if got := string(body); !strings.Contains(got, wantLogoHomeLink) {
		t.Errorf("register-pending body missing home-linked logo %q", wantLogoHomeLink)
	}
}
