//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// TestSuperAdmin_Integration covers the #319 super-admin backend: a
// regular admin cannot delete or reset scores on another admin's quiz and
// cannot reach the promote-super endpoint (it 404s to hide the route),
// while a super admin can delete and reset any quiz and can promote /
// demote other players. Demoting a super admin removes the elevated quiz
// powers immediately.
func TestSuperAdmin_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS": "super-owner@example.test," +
			"super-boss@example.test," +
			"super-plain@example.test",
	})
	baseURL := srv.BaseURL

	// owner creates the quizzes the other clients probe against. boss is
	// the super admin under test; plain stays a regular admin.
	owner := registerAdminClient(ctx, t, baseURL, srv.DBURI, "super-owner")
	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "super-boss")
	plain := registerAdminClient(ctx, t, baseURL, srv.DBURI, "super-plain")

	makeSuperAdmin(ctx, t, srv.DBURI, "super-boss")
	bossID := playerIDByUsername(ctx, t, srv.DBURI, "super-boss")
	plainID := playerIDByUsername(ctx, t, srv.DBURI, "super-plain")

	t.Run("regular admin cannot delete another admins quiz", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Delete Probe")
		status := postCSRFForm(ctx, t, plain, baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", quizID))
		if got, want := status, http.StatusForbidden; got != want {
			t.Errorf("delete status = %d, want %d", got, want)
		}
	})

	t.Run("regular admin cannot reset scores on another admins quiz", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Reset Probe")
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/players/%d/reset", quizID, bossID)
		if got, want := postCSRFForm(ctx, t, plain, target), http.StatusForbidden; got != want {
			t.Errorf("reset status = %d, want %d", got, want)
		}
	})

	t.Run("regular admin hitting promote-super gets 404", func(t *testing.T) {
		t.Parallel()
		target := baseURL + fmt.Sprintf("/admin/players/%d/promote-super", plainID)
		if got, want := postCSRFForm(ctx, t, plain, target), http.StatusNotFound; got != want {
			t.Errorf("promote-super status = %d, want %d", got, want)
		}
	})

	t.Run("super admin can delete any quiz", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Super Delete")
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/delete", quizID)
		if got, want := postCSRFForm(ctx, t, boss, target), http.StatusSeeOther; got != want {
			t.Errorf("super delete status = %d, want %d", got, want)
		}
	})

	t.Run("super admin can reset scores on any quiz", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Super Reset")
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/players/%d/reset", quizID, plainID)
		if got, want := postCSRFForm(ctx, t, boss, target), http.StatusSeeOther; got != want {
			t.Errorf("super reset status = %d, want %d", got, want)
		}
	})
}

// TestSuperAdmin_PromoteDemote_Integration exercises the full promote ->
// elevated-power -> demote -> power-removed lifecycle in one serial flow
// (#319). Kept separate from the parallel matrix in
// TestSuperAdmin_Integration because it mutates a player's super-admin
// flag mid-test, which parallel siblings must not observe.
func TestSuperAdmin_PromoteDemote_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "promote-owner@example.test,promote-boss@example.test,promote-demotee@example.test",
	})
	baseURL := srv.BaseURL

	owner := registerAdminClient(ctx, t, baseURL, srv.DBURI, "promote-owner")
	boss := registerAdminClient(ctx, t, baseURL, srv.DBURI, "promote-boss")
	demotee := registerAdminClient(ctx, t, baseURL, srv.DBURI, "promote-demotee")

	makeSuperAdmin(ctx, t, srv.DBURI, "promote-boss")
	demoteeID := playerIDByUsername(ctx, t, srv.DBURI, "promote-demotee")

	// boss promotes demotee to super admin.
	if got, want := postCSRFForm(ctx, t, boss,
		baseURL+fmt.Sprintf("/admin/players/%d/promote-super", demoteeID),
	), http.StatusSeeOther; got != want {
		t.Fatalf("promote status = %d, want %d", got, want)
	}
	if got, want := isSuperAdmin(ctx, t, srv.DBURI, "promote-demotee"), true; got != want {
		t.Fatalf("after promote is_super_admin = %v, want %v", got, want)
	}

	// As a super admin, demotee can now delete the owner's quiz.
	superQuizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Demotee Probe")
	if got, want := postCSRFForm(ctx, t, demotee,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", superQuizID),
	), http.StatusSeeOther; got != want {
		t.Fatalf("demotee super delete status = %d, want %d", got, want)
	}

	// boss demotes demotee.
	if got, want := postCSRFForm(ctx, t, boss,
		baseURL+fmt.Sprintf("/admin/players/%d/demote-super", demoteeID),
	), http.StatusSeeOther; got != want {
		t.Fatalf("demote status = %d, want %d", got, want)
	}
	if got, want := isSuperAdmin(ctx, t, srv.DBURI, "promote-demotee"), false; got != want {
		t.Fatalf("after demote is_super_admin = %v, want %v", got, want)
	}

	// Powers are gone immediately: deleting another admin's quiz now 403s.
	probeQuizID := createQuizAs(ctx, t, owner, baseURL, "Owner Quiz Post-Demote Probe")
	if got, want := postCSRFForm(ctx, t, demotee,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", probeQuizID),
	), http.StatusForbidden; got != want {
		t.Errorf("post-demote delete status = %d, want %d", got, want)
	}
}

// makeSuperAdmin promotes the named player to super admin via the store,
// matching how the production promote endpoint mutates the row. Used to
// bootstrap the super admin under test, mirroring the CLI bootstrap path.
func makeSuperAdmin(ctx context.Context, t *testing.T, dbURI, username string) {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByUsername(ctx, username)
	if err != nil {
		t.Fatalf("makeSuperAdmin GetPlayerByUsername err = %v, want nil", err)
	}
	if err := stores.AdminPlayers.SetPlayerSuperAdmin(ctx, player.ID, true); err != nil {
		t.Fatalf("makeSuperAdmin SetPlayerSuperAdmin err = %v, want nil", err)
	}
}

// isSuperAdmin reads the current is_super_admin flag for the named player
// through the auth.Player mapping so the test pins the persisted state.
func isSuperAdmin(ctx context.Context, t *testing.T, dbURI, username string) bool {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByUsername(ctx, username)
	if err != nil {
		t.Fatalf("isSuperAdmin GetPlayerByUsername err = %v, want nil", err)
	}

	return player.IsSuperAdmin
}

// playerIDByUsername returns the players.id for the named player.
func playerIDByUsername(ctx context.Context, t *testing.T, dbURI, username string) int64 {
	t.Helper()
	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByUsername(ctx, username)
	if err != nil {
		t.Fatalf("playerIDByUsername GetPlayerByUsername err = %v, want nil", err)
	}

	return player.ID
}

// postCSRFForm fetches a fresh CSRF token (the cookie is Path=/, so any
// admin GET page seeds a session-scoped token) and posts an empty form to
// target, returning the status code. The probes only assert on the
// status, so this keeps the call sites short while reusing the shared
// body-closing [postForm].
func postCSRFForm(ctx context.Context, t *testing.T, client *http.Client, target string) int {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, srvBaseURL(t, target)+"/admin")
	status, _, _ := postForm(ctx, t, client, target, url.Values{"csrf_token": {token}})

	return status
}

// srvBaseURL extracts the scheme://host base from an absolute target URL
// so postCSRFForm can reach the always-available /admin page for a CSRF
// token regardless of which endpoint it is about to post to.
func srvBaseURL(t *testing.T, target string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("url.Parse(%q) err = %v, want nil", target, err)
	}

	return u.Scheme + "://" + u.Host
}
