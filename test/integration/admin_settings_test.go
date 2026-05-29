//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// TestAdminSettings_Integration covers the #320/#538 Admin settings page: a
// Host gets a 404 (the route stays hidden), an Admin gets 200 and sees the
// Admins list, promoting a player via the id-based role endpoint makes them
// appear on reload, and demoting them through the same endpoint removes them
// again.
func TestAdminSettings_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "settings-boss@example.test",
	})
	baseURL := srv.BaseURL

	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-boss")
	host := registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-host")
	registerAdminClient(ctx, t, baseURL, srv.DBURI, "settings-target")

	// settings-boss lands on Admin via ADMIN_EMAILS; demote the rest off the
	// first-registrant promotion to Host / Player so the gate is meaningful.
	makeHost(ctx, t, srv.DBURI, "settings-host")
	makeHost(ctx, t, srv.DBURI, "settings-target")

	t.Run("host gets 404", func(t *testing.T) {
		t.Parallel()
		resp := getWith(ctx, t, host, baseURL+"/admin/settings")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("settings status for host = %d, want %d", got, want)
		}
	})

	t.Run("admin sees the list and can promote then demote", func(t *testing.T) {
		t.Parallel()
		// The admin sees the page and their own name in the list.
		body := getSettingsBody(ctx, t, boss, baseURL)
		if !strings.Contains(body, "settings-boss") {
			t.Fatalf("settings page does not list the admin; body=%q", body)
		}
		if strings.Contains(body, "settings-target") {
			t.Fatalf("target appears as admin before promotion; body=%q", body)
		}

		// Promote the target to admin via the id-based role endpoint.
		targetID := playerIDByUsername(ctx, t, srv.DBURI, "settings-target")
		if got, want := postCSRFRoleForm(ctx, t, boss,
			baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), auth.RoleAdmin,
		), http.StatusSeeOther; got != want {
			t.Fatalf("promote status = %d, want %d", got, want)
		}
		if got, want := roleByUsername(ctx, t, srv.DBURI, "settings-target"), auth.RoleAdmin; got != want {
			t.Fatalf("after promote role = %q, want %q", got, want)
		}

		// On reload the target now appears in the list.
		if body := getSettingsBody(ctx, t, boss, baseURL); !strings.Contains(body, "settings-target") {
			t.Fatalf("target missing from list after promote; body=%q", body)
		}

		// Demote back to host via the same endpoint, mirroring the settings
		// page's demote button.
		if got, want := postCSRFRoleForm(ctx, t, boss,
			baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), auth.RoleHost,
		), http.StatusSeeOther; got != want {
			t.Fatalf("demote status = %d, want %d", got, want)
		}
		if got, want := roleByUsername(ctx, t, srv.DBURI, "settings-target"), auth.RoleHost; got != want {
			t.Fatalf("after demote role = %q, want %q", got, want)
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
