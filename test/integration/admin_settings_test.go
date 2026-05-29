//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAdminSettings_Integration covers the #320 super-admin settings page
// after the #527 rework: a regular admin gets a 404 (the route stays
// hidden), a super admin gets 200 and sees the super-admin list, promoting
// a player via the id-based role endpoint makes them appear on reload, and
// demoting them through the same endpoint removes them again.
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

		// Promote the target to super admin via the id-based role endpoint.
		targetID := playerIDByUsername(ctx, t, srv.DBURI, "settings-target")
		if got, want := postCSRFRoleForm(ctx, t, boss,
			baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), "super_admin",
		), http.StatusSeeOther; got != want {
			t.Fatalf("promote status = %d, want %d", got, want)
		}
		if got, want := isSuperAdmin(ctx, t, srv.DBURI, "settings-target"), true; got != want {
			t.Fatalf("after promote is_super_admin = %v, want %v", got, want)
		}

		// On reload the target now appears in the list.
		if body := getSettingsBody(ctx, t, boss, baseURL); !strings.Contains(body, "settings-target") {
			t.Fatalf("target missing from list after promote; body=%q", body)
		}

		// Demote back to plain admin via the same endpoint, mirroring the
		// settings page's demote button.
		if got, want := postCSRFRoleForm(ctx, t, boss,
			baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), "admin",
		), http.StatusSeeOther; got != want {
			t.Fatalf("demote status = %d, want %d", got, want)
		}
		if got, want := isSuperAdmin(ctx, t, srv.DBURI, "settings-target"), false; got != want {
			t.Fatalf("after demote is_super_admin = %v, want %v", got, want)
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
