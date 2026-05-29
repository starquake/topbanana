//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAdminSettings_Integration covers the #320 super-admin settings page:
// a regular admin gets a 404 (the route stays hidden), a super admin gets
// 200 and sees the super-admin list, promoting a player by username makes
// them appear on reload, and demoting them removes them again.
func TestAdminSettings_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS": "settings-boss@example.test," +
			"settings-plain@example.test," +
			"settings-target@example.test",
	})
	baseURL := srv.BaseURL

	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-boss")
	plain := registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-plain")
	registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-target")

	makeSuperAdmin(ctx, t, srv.DBURI, "settings-boss")

	t.Run("regular admin gets 404", func(t *testing.T) {
		t.Parallel()
		resp := getWith(ctx, t, plain, baseURL+"/admin/settings")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("settings status for regular admin = %d, want %d", got, want)
		}
	})

	t.Run("super admin sees the list and can promote then demote", func(t *testing.T) {
		t.Parallel()
		// The super admin sees the page and their own name in the list.
		body := getSettingsBody(ctx, t, boss, baseURL)
		if !strings.Contains(body, "settings-boss") {
			t.Fatalf("settings page does not list the super admin; body=%q", body)
		}
		if strings.Contains(body, "settings-target") {
			t.Fatalf("target appears as super admin before promotion; body=%q", body)
		}

		// Promote the target by username via the settings form.
		token := fetchCSRFToken(ctx, t, boss, baseURL+"/admin/settings")
		status, loc, _ := postForm(ctx, t, boss, baseURL+"/admin/settings/promote",
			url.Values{"csrf_token": {token}, "username": {"settings-target"}})
		if got, want := status, http.StatusSeeOther; got != want {
			t.Fatalf("promote status = %d, want %d", got, want)
		}
		if got, want := loc, "/admin/settings"; got != want {
			t.Errorf("promote redirect = %q, want %q", got, want)
		}
		if got, want := isSuperAdmin(ctx, t, srv.DBURI, "settings-target"), true; got != want {
			t.Fatalf("after promote is_super_admin = %v, want %v", got, want)
		}

		// On reload the target now appears in the list.
		if body := getSettingsBody(ctx, t, boss, baseURL); !strings.Contains(body, "settings-target") {
			t.Fatalf("target missing from list after promote; body=%q", body)
		}

		// Demote via the id-based row button endpoint, mirroring the
		// template's demote form.
		targetID := playerIDByUsername(ctx, t, srv.DBURI, "settings-target")
		token = fetchCSRFToken(ctx, t, boss, baseURL+"/admin/settings")
		status, _, _ = postForm(ctx, t, boss,
			baseURL+fmt.Sprintf("/admin/players/%d/demote-super", targetID),
			url.Values{"csrf_token": {token}})
		if got, want := status, http.StatusSeeOther; got != want {
			t.Fatalf("demote status = %d, want %d", got, want)
		}
		if got, want := isSuperAdmin(ctx, t, srv.DBURI, "settings-target"), false; got != want {
			t.Fatalf("after demote is_super_admin = %v, want %v", got, want)
		}
	})

	t.Run("promote with unknown username flashes not-found", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, boss, baseURL+"/admin/settings")
		status, _, _ := postForm(ctx, t, boss, baseURL+"/admin/settings/promote",
			url.Values{"csrf_token": {token}, "username": {"no-such-player"}})
		if got, want := status, http.StatusSeeOther; got != want {
			t.Fatalf("promote-unknown status = %d, want %d", got, want)
		}
	})
}

// getSettingsBody fetches GET /admin/settings as client, asserts a 200,
// and returns the rendered HTML body.
func getSettingsBody(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp := getWith(ctx, t, client, baseURL+"/admin/settings")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("settings status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}
