package admin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// roleStubStore implements auth.AdminPlayerStore for the HandlePlayerSetRole
// tests. Only the methods the handler touches do anything useful; the rest
// return a sentinel so an accidental call is loud.
type roleStubStore struct {
	detail     *auth.PlayerDetail
	adminCount int64

	setRole   string
	setCalled bool

	auditAction  string
	auditPayload string
	auditCalled  bool
}

func (s *roleStubStore) GetPlayerDetail(_ context.Context, _ int64) (*auth.PlayerDetail, error) {
	if s.detail == nil {
		return nil, auth.ErrPlayerNotFound
	}

	return s.detail, nil
}

func (s *roleStubStore) CountAdmins(_ context.Context) (int64, error) {
	return s.adminCount, nil
}

func (s *roleStubStore) SetPlayerRole(_ context.Context, _ int64, role string) error {
	s.setCalled = true
	s.setRole = role

	return nil
}

func (s *roleStubStore) InsertAdminAudit(
	_ context.Context, _, _ int64, action, payload string,
) error {
	s.auditCalled = true
	s.auditAction = action
	s.auditPayload = payload

	return nil
}

func (*roleStubStore) ListRecentFinishedGamesForPlayer(
	_ context.Context, _, _ int64,
) ([]*auth.RecentFinishedGame, error) {
	return nil, errors.ErrUnsupported
}

func (*roleStubStore) SetPlayerEmailVerifiedNow(_ context.Context, _ int64) error {
	return errors.ErrUnsupported
}

func (*roleStubStore) SetPlayerEmail(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*roleStubStore) CreatePlayerByAdmin(
	_ context.Context, _, _, _ string,
) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*roleStubStore) ListAdminAuditForTarget(
	_ context.Context, _, _ int64,
) ([]*auth.AdminAuditEntry, error) {
	return nil, errors.ErrUnsupported
}

func (*roleStubStore) RenamePlayer(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*roleStubStore) ChangePlayerPassword(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

// postRole drives HandlePlayerSetRole with the given current detail and
// desired role, returning the recorded stub state plus the response.
func postRole(
	t *testing.T, store *roleStubStore, desired string,
) *httptest.ResponseRecorder {
	t.Helper()
	flash := auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
	handler := HandlePlayerSetRole(slog.New(slog.DiscardHandler), store, flash)

	form := url.Values{"role": {desired}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/7/role",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", "7")
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: 1, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerSetRole_UnknownRoleRejected(t *testing.T) {
	t.Parallel()

	store := &roleStubStore{
		detail:     &auth.PlayerDetail{ID: 7, Role: auth.RolePlayer},
		adminCount: 1,
	}

	rec := postRole(t, store, "wizard")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.setCalled {
		t.Error("SetPlayerRole called, want no mutation on unknown role")
	}
	if store.auditCalled {
		t.Error("audit written, want none on unknown role")
	}
}

func TestHandlePlayerSetRole_LastAdminGuard(t *testing.T) {
	t.Parallel()

	store := &roleStubStore{
		detail:     &auth.PlayerDetail{ID: 7, Role: auth.RoleAdmin},
		adminCount: 1,
	}

	rec := postRole(t, store, "host")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.setCalled {
		t.Error("SetPlayerRole called, want refusal when demoting the last admin")
	}
	if store.auditCalled {
		t.Error("audit written, want none when the guard fires")
	}
}

func TestHandlePlayerSetRole_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()

	store := &roleStubStore{
		detail:     &auth.PlayerDetail{ID: 7, Role: auth.RoleAdmin},
		adminCount: 2,
	}

	rec := postRole(t, store, "admin")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.setCalled {
		t.Error("SetPlayerRole called, want no mutation when the role is unchanged")
	}
	if store.auditCalled {
		t.Error("audit written, want none on a no-op")
	}
}

func TestHandlePlayerSetRole_TransitionsWriteRoleChangedAudit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		from       *auth.PlayerDetail
		adminCount int64
		desired    string
		wantRole   string
		wantDetail string
	}{
		{
			name:       "player to host",
			from:       &auth.PlayerDetail{ID: 7, Role: auth.RolePlayer},
			adminCount: 1,
			desired:    auth.RoleHost,
			wantRole:   auth.RoleHost,
			wantDetail: `"from":"player","to":"host"`,
		},
		{
			name:       "host to admin",
			from:       &auth.PlayerDetail{ID: 7, Role: auth.RoleHost},
			adminCount: 1,
			desired:    auth.RoleAdmin,
			wantRole:   auth.RoleAdmin,
			wantDetail: `"from":"host","to":"admin"`,
		},
		{
			name:       "admin to host with another admin present",
			from:       &auth.PlayerDetail{ID: 7, Role: auth.RoleAdmin},
			adminCount: 2,
			desired:    auth.RoleHost,
			wantRole:   auth.RoleHost,
			wantDetail: `"from":"admin","to":"host"`,
		},
		{
			name:       "host to player",
			from:       &auth.PlayerDetail{ID: 7, Role: auth.RoleHost},
			adminCount: 1,
			desired:    auth.RolePlayer,
			wantRole:   auth.RolePlayer,
			wantDetail: `"from":"host","to":"player"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &roleStubStore{detail: tc.from, adminCount: tc.adminCount}

			rec := postRole(t, store, tc.desired)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if !store.setCalled {
				t.Fatal("SetPlayerRole not called, want the transition applied")
			}
			if got, want := store.setRole, tc.wantRole; got != want {
				t.Errorf("set role = %q, want %q", got, want)
			}
			if got, want := store.auditAction, auth.AdminActionRoleChanged; got != want {
				t.Errorf("audit action = %q, want %q", got, want)
			}
			if got, want := store.auditPayload, tc.wantDetail; !strings.Contains(got, want) {
				t.Errorf("audit payload = %q, should contain %q", got, want)
			}
		})
	}
}
