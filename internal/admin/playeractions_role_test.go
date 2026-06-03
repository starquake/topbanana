//go:build integration

package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// seedPlayerWithRole creates a player and stamps the given role, so the
// role-change handler's current-role diff and last-admin guard read real
// rows. A fresh migrated DB has no admins (the legacy seeded row backfills
// to 'player' in migration 20260529160000), so admin count is whatever
// these helpers create.
func (e *adminEnv) seedPlayerWithRole(t *testing.T, displayName, role string) int64 {
	t.Helper()

	id := e.seedPlayer(t, displayName)
	if err := e.admin.SetPlayerRole(t.Context(), id, role); err != nil {
		t.Fatalf("SetPlayerRole(%d, %q) err = %v, want nil", id, role, err)
	}

	return id
}

// postRole drives HandlePlayerSetRole against the target player with the
// desired role, returning the response recorder.
func postRole(
	t *testing.T, env *adminEnv, targetID int64, desired string,
) *httptest.ResponseRecorder {
	t.Helper()
	flash := auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
	handler := HandlePlayerSetRole(slog.New(slog.DiscardHandler), env.admin, flash)

	form := url.Values{"role": {desired}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(targetID, 10)+"/role",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(targetID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// roleOf reloads the target's persisted role.
func (e *adminEnv) roleOf(t *testing.T, targetID int64) string {
	t.Helper()

	detail, err := e.admin.GetPlayerDetail(t.Context(), targetID)
	if err != nil {
		t.Fatalf("GetPlayerDetail(%d) err = %v, want nil", targetID, err)
	}

	return detail.Role
}

func TestHandlePlayerSetRole_UnknownRoleRejected(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedPlayerWithRole(t, "target", auth.RolePlayer)

	rec := postRole(t, env, target, "wizard")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RolePlayer; got != want {
		t.Errorf("role = %q, want %q (no mutation on unknown role)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on unknown role", got, want)
	}
}

func TestHandlePlayerSetRole_LastAdminGuard(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// The target is the only admin, so demoting it must be refused.
	target := env.seedPlayerWithRole(t, "only-admin", auth.RoleAdmin)

	rec := postRole(t, env, target, auth.RoleHost)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleAdmin; got != want {
		t.Errorf("role = %q, want %q (refused demotion of the last admin)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d when the guard fires", got, want)
	}
}

func TestHandlePlayerSetRole_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// A second admin exists so the no-op path is reached on its own merits,
	// not because the guard would otherwise block.
	env.seedPlayerWithRole(t, "other-admin", auth.RoleAdmin)
	target := env.seedPlayerWithRole(t, "target-admin", auth.RoleAdmin)

	rec := postRole(t, env, target, auth.RoleAdmin)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleAdmin; got != want {
		t.Errorf("role = %q, want %q (unchanged)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on a no-op", got, want)
	}
}

func TestHandlePlayerSetRole_TransitionsWriteRoleChangedAudit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from string
		// secondAdmin seeds an extra admin so the last-admin guard does not
		// block a demotion away from admin.
		secondAdmin bool
		desired     string
		wantRole    string
		wantDetail  string
	}{
		{
			name:       "player to host",
			from:       auth.RolePlayer,
			desired:    auth.RoleHost,
			wantRole:   auth.RoleHost,
			wantDetail: `"from":"player","to":"host"`,
		},
		{
			name:       "host to admin",
			from:       auth.RoleHost,
			desired:    auth.RoleAdmin,
			wantRole:   auth.RoleAdmin,
			wantDetail: `"from":"host","to":"admin"`,
		},
		{
			name:        "admin to host with another admin present",
			from:        auth.RoleAdmin,
			secondAdmin: true,
			desired:     auth.RoleHost,
			wantRole:    auth.RoleHost,
			wantDetail:  `"from":"admin","to":"host"`,
		},
		{
			name:       "host to player",
			from:       auth.RoleHost,
			desired:    auth.RolePlayer,
			wantRole:   auth.RolePlayer,
			wantDetail: `"from":"host","to":"player"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newAdminEnv(t)
			if tc.secondAdmin {
				env.seedPlayerWithRole(t, "other-admin", auth.RoleAdmin)
			}
			target := env.seedPlayerWithRole(t, "target", tc.from)

			rec := postRole(t, env, target, tc.desired)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := env.roleOf(t, target), tc.wantRole; got != want {
				t.Errorf("role = %q, want %q", got, want)
			}
			entries := env.auditEntries(t, target)
			if got, want := len(entries), 1; got != want {
				t.Fatalf("audit entries = %d, want %d", got, want)
			}
			if got, want := entries[0].Action, auth.AdminActionRoleChanged; got != want {
				t.Errorf("audit action = %q, want %q", got, want)
			}
			if got, want := entries[0].Payload, tc.wantDetail; !strings.Contains(got, want) {
				t.Errorf("audit payload = %q, should contain %q", got, want)
			}
		})
	}
}
