package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// TestRoles_HostGating covers the #538 re-gating: a Host reaches the
// dashboard and can manage only their OWN quizzes (not another host's),
// while the player-management and email-diagnostics routes 404 for a Host
// (they moved up to Admin-only). An Admin can do all of it; a Player is
// bounced from the dashboard.
func TestRoles_HostGating(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL

	// boss registers first and stays Admin (first credentialled registrant).
	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "roles-boss")
	owner := registerAdminClient(ctx, t, baseURL, srv.DBURI, "roles-owner")
	host := registerAdminClient(ctx, t, baseURL, srv.DBURI, "roles-host")
	player := registerAdminClient(ctx, t, baseURL, srv.DBURI, "roles-player")

	makeHost(ctx, t, srv.DBURI, "roles-owner")
	makeHost(ctx, t, srv.DBURI, "roles-host")
	// roles-player keeps the default Player tier.

	ownerQuiz := createQuizAs(ctx, t, owner, baseURL, "Owner Host Quiz")
	hostID := playerIDByDisplayName(ctx, t, srv.DBURI, "roles-host")

	t.Run("host reaches the dashboard", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, host, baseURL+"/admin").StatusCode, http.StatusOK; got != want {
			t.Errorf("host GET /admin status = %d, want %d", got, want)
		}
	})

	t.Run("player is bounced from the dashboard", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, player, baseURL+"/admin").StatusCode, http.StatusForbidden; got != want {
			t.Errorf("player GET /admin status = %d, want %d", got, want)
		}
	})

	t.Run("host manages own quiz", func(t *testing.T) {
		t.Parallel()
		myQuiz := createQuizAs(ctx, t, host, baseURL, "Host Own Quiz")
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/delete", myQuiz)
		if got, want := postCSRFForm(ctx, t, host, target), http.StatusSeeOther; got != want {
			t.Errorf("host delete own quiz status = %d, want %d", got, want)
		}
	})

	t.Run("host cannot manage another host's quiz", func(t *testing.T) {
		t.Parallel()
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/delete", ownerQuiz)
		if got, want := postCSRFForm(ctx, t, host, target), http.StatusForbidden; got != want {
			t.Errorf("host delete other quiz status = %d, want %d", got, want)
		}
	})

	t.Run("host is 404 on player management", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, host, baseURL+"/admin/players").StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host GET /admin/players status = %d, want %d", got, want)
		}
		detail := baseURL + fmt.Sprintf("/admin/players/%d", hostID)
		if got, want := doGet(ctx, t, host, detail).StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host GET player detail status = %d, want %d", got, want)
		}
	})

	t.Run("host is 404 on email diagnostics", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, host, baseURL+"/admin/email").StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host GET /admin/email status = %d, want %d", got, want)
		}
	})

	t.Run("host is 404 on settings", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, host, baseURL+"/admin/settings").StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host GET /admin/settings status = %d, want %d", got, want)
		}
	})

	t.Run("admin reaches player management, email, and settings", func(t *testing.T) {
		t.Parallel()
		if got, want := doGet(ctx, t, boss, baseURL+"/admin/players").StatusCode, http.StatusOK; got != want {
			t.Errorf("admin GET /admin/players status = %d, want %d", got, want)
		}
		if got, want := doGet(ctx, t, boss, baseURL+"/admin/email").StatusCode, http.StatusOK; got != want {
			t.Errorf("admin GET /admin/email status = %d, want %d", got, want)
		}
		if got, want := doGet(ctx, t, boss, baseURL+"/admin/settings").StatusCode, http.StatusOK; got != want {
			t.Errorf("admin GET /admin/settings status = %d, want %d", got, want)
		}
	})

	t.Run("admin can delete any host's quiz", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Admin Delete")
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/delete", quizID)
		if got, want := postCSRFForm(ctx, t, boss, target), http.StatusSeeOther; got != want {
			t.Errorf("admin delete other quiz status = %d, want %d", got, want)
		}
	})
}

// TestRoles_RoleChange exercises the player -> admin -> host lifecycle
// through the id-based role endpoint (#538), pins the role_changed audit
// row, and confirms an admin gains/loses elevated quiz powers as the role
// moves.
func TestRoles_RoleChange(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "rc-boss@example.test",
	})
	baseURL := srv.BaseURL

	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "rc-boss")
	owner := registerAdminClient(ctx, t, baseURL, srv.DBURI, "rc-owner")
	target := registerAdminClient(ctx, t, baseURL, srv.DBURI, "rc-target")

	makeHost(ctx, t, srv.DBURI, "rc-owner")
	bossID := playerIDByDisplayName(ctx, t, srv.DBURI, "rc-boss")
	targetID := playerIDByDisplayName(ctx, t, srv.DBURI, "rc-target")

	// boss promotes target player -> admin.
	if got, want := postCSRFRoleForm(ctx, t, boss,
		baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), auth.RoleAdmin,
	), http.StatusSeeOther; got != want {
		t.Fatalf("promote-to-admin status = %d, want %d", got, want)
	}
	if got, want := roleByDisplayName(ctx, t, srv.DBURI, "rc-target"), auth.RoleAdmin; got != want {
		t.Fatalf("after promote role = %q, want %q", got, want)
	}
	assertAuditRow(ctx, t, srv.DBURI, targetID, bossID, auth.AdminActionRoleChanged)

	// As an Admin, target can now delete the owner host's quiz.
	ownerQuiz := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz RC Probe")
	if got, want := postCSRFForm(ctx, t, target,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", ownerQuiz),
	), http.StatusSeeOther; got != want {
		t.Fatalf("admin delete status = %d, want %d", got, want)
	}

	// boss demotes target back to host.
	if got, want := postCSRFRoleForm(ctx, t, boss,
		baseURL+fmt.Sprintf("/admin/players/%d/role", targetID), auth.RoleHost,
	), http.StatusSeeOther; got != want {
		t.Fatalf("demote-to-host status = %d, want %d", got, want)
	}
	if got, want := roleByDisplayName(ctx, t, srv.DBURI, "rc-target"), auth.RoleHost; got != want {
		t.Fatalf("after demote role = %q, want %q", got, want)
	}

	// Powers gone: deleting another host's quiz now 403s.
	probeQuiz := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Post-Demote")
	if got, want := postCSRFForm(ctx, t, target,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", probeQuiz),
	), http.StatusForbidden; got != want {
		t.Errorf("post-demote delete status = %d, want %d", got, want)
	}
}

// TestRoles_LastAdminGuard pins the last-admin guard (#538): the sole Admin
// cannot demote themselves (the change is refused and the row stays Admin),
// but once a second Admin exists either can be demoted.
func TestRoles_LastAdminGuard(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "guard-solo@example.test",
	})
	baseURL := srv.BaseURL

	solo := registerAdminClient(ctx, t, baseURL, srv.DBURI, "guard-solo")
	registerAdminClient(ctx, t, baseURL, srv.DBURI, "guard-second")

	soloID := playerIDByDisplayName(ctx, t, srv.DBURI, "guard-solo")
	secondID := playerIDByDisplayName(ctx, t, srv.DBURI, "guard-second")

	// The sole Admin cannot demote themselves: refused (303 back with a
	// flash) and the row stays Admin.
	if got, want := postCSRFRoleForm(ctx, t, solo,
		baseURL+fmt.Sprintf("/admin/players/%d/role", soloID), auth.RoleHost,
	), http.StatusSeeOther; got != want {
		t.Fatalf("self-demote status = %d, want %d", got, want)
	}
	if got, want := roleByDisplayName(ctx, t, srv.DBURI, "guard-solo"), auth.RoleAdmin; got != want {
		t.Fatalf("after refused self-demote role = %q, want %q", got, want)
	}

	// With a second Admin in place, a demote is allowed again.
	if got, want := postCSRFRoleForm(ctx, t, solo,
		baseURL+fmt.Sprintf("/admin/players/%d/role", secondID), auth.RoleAdmin,
	), http.StatusSeeOther; got != want {
		t.Fatalf("promote second status = %d, want %d", got, want)
	}
	if got, want := roleByDisplayName(ctx, t, srv.DBURI, "guard-second"), auth.RoleAdmin; got != want {
		t.Fatalf("after promote second role = %q, want %q", got, want)
	}
	if got, want := postCSRFRoleForm(ctx, t, solo,
		baseURL+fmt.Sprintf("/admin/players/%d/role", secondID), auth.RoleHost,
	), http.StatusSeeOther; got != want {
		t.Fatalf("demote second status = %d, want %d", got, want)
	}
	if got, want := roleByDisplayName(ctx, t, srv.DBURI, "guard-second"), auth.RoleHost; got != want {
		t.Errorf("after demote second role = %q, want %q", got, want)
	}
}

// assertAuditRow fails the test unless an admin_audit row targeting target
// with the given action and acting actor exists. Reads through the same
// store path the #450 detail view uses.
func assertAuditRow(ctx context.Context, t *testing.T, dbURI string, target, actor int64, action string) {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	const auditLimit = 50
	entries, err := stores.AdminPlayers.ListAdminAuditForTarget(ctx, target, auditLimit)
	if err != nil {
		t.Fatalf("ListAdminAuditForTarget err = %v, want nil", err)
	}
	for _, e := range entries {
		if e.Action == action && e.ActorPlayerID == actor {
			return
		}
	}
	t.Errorf("no admin_audit row with action %q and actor %d found for target %d; entries=%+v",
		action, actor, target, entries)
}

// makeHost sets the named player's role to Host via the store, mirroring how
// the production role endpoint mutates the row. Used to set up Host fixtures
// (the only non-default tier the integration suite seeds directly; Admin comes
// from ADMIN_EMAILS / first-registrant promotion).
func makeHost(ctx context.Context, t *testing.T, dbURI, displayName string) {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("makeHost GetPlayerByDisplayName err = %v, want nil", err)
	}
	if err := stores.AdminPlayers.SetPlayerRole(ctx, player.ID, auth.RoleHost); err != nil {
		t.Fatalf("makeHost SetPlayerRole err = %v, want nil", err)
	}
}

// roleByDisplayName reads the current role for the named player through the
// auth.Player mapping so the test pins the persisted state.
func roleByDisplayName(ctx context.Context, t *testing.T, dbURI, displayName string) string {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("roleByDisplayName GetPlayerByDisplayName err = %v, want nil", err)
	}

	return player.Role
}

// playerIDByDisplayName returns the players.id for the named player.
func playerIDByDisplayName(ctx context.Context, t *testing.T, dbURI, displayName string) int64 {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("playerIDByDisplayName GetPlayerByDisplayName err = %v, want nil", err)
	}

	return player.ID
}

// postCSRFForm fetches a fresh CSRF token (the cookie is Path=/, so any
// admin GET page seeds a session-scoped token) and posts an empty form to
// target, returning the status code. The probes only assert on the status,
// so this keeps the call sites short while reusing the shared body-closing
// [postForm].
func postCSRFForm(ctx context.Context, t *testing.T, client *http.Client, target string) int {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, srvBaseURL(t, target)+"/admin")
	status, _, _ := postForm(ctx, t, client, target, url.Values{"csrf_token": {token}})

	return status
}

// postCSRFRoleForm posts the id-based role endpoint (#538) with the given
// role and a fresh CSRF token, returning the status code. Mirrors
// postCSRFForm but carries the "role" field the handler diffs against.
func postCSRFRoleForm(
	ctx context.Context, t *testing.T, client *http.Client, target, role string,
) int {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, srvBaseURL(t, target)+"/admin")
	status, _, _ := postForm(ctx, t, client, target, url.Values{
		"csrf_token": {token},
		"role":       {role},
	})

	return status
}

// srvBaseURL extracts the scheme://host base from an absolute target URL so
// postCSRFForm can reach the always-available /admin page for a CSRF token
// regardless of which endpoint it is about to post to.
func srvBaseURL(t *testing.T, target string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("url.Parse(%q) err = %v, want nil", target, err)
	}

	return u.Scheme + "://" + u.Host
}
