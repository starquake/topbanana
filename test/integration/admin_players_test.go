//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAdminPlayersList_RequiresAdmin pins the RequireAdmin gate on
// /admin/players (#423): an anonymous visitor lands on /login rather
// than the player list.
func TestAdminPlayersList_RequiresAdmin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	client := authClient(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.BaseURL+"/admin/players", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
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

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/login"; !strings.HasPrefix(got, want) {
		t.Errorf("Location = %q, want prefix %q", got, want)
	}
}

// TestAdminPlayersList_RendersForAdmin pins the happy path: an admin
// session sees the list page with both account-type labels rendered
// (admin for the promoted first registrant, password for the second).
func TestAdminPlayersList_RendersForAdmin(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	// First registrant gets promoted to admin so the second one stays a
	// plain password player — we need both labels in the rendered table
	// to pin the AccountType derivation end-to-end.
	adminClient := authClient(t)
	if got := registerForRedirect(
		ctx, t, adminClient, srv.BaseURL,
		"players-admin", "players-admin-pass-123",
	); got != "/admin/quizzes" {
		t.Fatalf("first registration did not promote to admin: Location = %q", got)
	}
	playerClient := authClient(t)
	if got := registerForRedirect(
		ctx, t, playerClient, srv.BaseURL,
		"players-player", "players-player-pass-123",
	); got != "/" {
		t.Fatalf("second registration did not stay player: Location = %q", got)
	}

	body := getOK(ctx, t, adminClient, srv.BaseURL+"/admin/players")

	for _, want := range []string{
		"Players",
		"players-admin",
		"players-player",
		"password",
		"Account type",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body should contain %q; body=%q", want, body)
		}
	}
}

// TestAdminPlayersList_PaginationParam covers the ?page=N branch: an
// out-of-range page collapses back to the last available page (the
// list never 404s on a hand-edited URL).
func TestAdminPlayersList_PaginationParam(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	client := authClient(t)
	if got := registerForRedirect(
		ctx,
		t,
		client,
		srv.BaseURL,
		"page-admin",
		"page-admin-pass-123",
	); got != "/admin/quizzes" {
		t.Fatalf("registration did not promote to admin: Location = %q", got)
	}

	// A wildly out-of-range page parameter must clamp to the last
	// page (here, the only page) rather than 404 or 500. We assert
	// the page still rendered the table header and the admin row.
	body := getOK(ctx, t, client, srv.BaseURL+"/admin/players?page=99")
	for _, want := range []string{"Account type", "page-admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("body should contain %q after clamping page; body=%q", want, body)
		}
	}
}

// getOK GETs the URL with the given client and returns the body string,
// failing the test on any error or non-2xx status.
func getOK(ctx context.Context, t *testing.T, client *http.Client, url string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
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
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET %s status = %d, want %d", url, got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}
