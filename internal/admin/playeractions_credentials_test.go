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

// credStubStore implements auth.AdminPlayerStore for the
// HandlePlayerSetDisplayName / HandlePlayerSetPassword tests. Only the methods
// those handlers touch record anything; the rest return a sentinel so an
// accidental call is loud.
type credStubStore struct {
	renameErr error
	passErr   error

	renameName   string
	renameCalled bool

	passHash   string
	passCalled bool

	auditAction  string
	auditPayload string
	auditCalled  bool
}

func (s *credStubStore) RenamePlayer(_ context.Context, _ int64, displayName string) (*auth.Player, error) {
	s.renameCalled = true
	s.renameName = displayName
	if s.renameErr != nil {
		return nil, s.renameErr
	}

	return &auth.Player{ID: 7, DisplayName: displayName}, nil
}

func (s *credStubStore) ChangePlayerPassword(_ context.Context, _ int64, passwordHash string) error {
	s.passCalled = true
	s.passHash = passwordHash

	return s.passErr
}

func (s *credStubStore) InsertAdminAudit(
	_ context.Context, _, _ int64, action, payload string,
) error {
	s.auditCalled = true
	s.auditAction = action
	s.auditPayload = payload

	return nil
}

func (*credStubStore) GetPlayerDetail(_ context.Context, _ int64) (*auth.PlayerDetail, error) {
	return nil, errors.ErrUnsupported
}

func (*credStubStore) ListRecentFinishedGamesForPlayer(
	_ context.Context, _, _ int64,
) ([]*auth.RecentFinishedGame, error) {
	return nil, errors.ErrUnsupported
}

func (*credStubStore) SetPlayerEmailVerifiedNow(_ context.Context, _ int64) error {
	return errors.ErrUnsupported
}

func (*credStubStore) SetPlayerEmail(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*credStubStore) CreatePlayerByAdmin(
	_ context.Context, _, _, _ string,
) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*credStubStore) SetPlayerRole(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*credStubStore) CountAdmins(_ context.Context) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (*credStubStore) ListAdminAuditForTarget(
	_ context.Context, _, _ int64,
) ([]*auth.AdminAuditEntry, error) {
	return nil, errors.ErrUnsupported
}

func newCredFlash(t *testing.T) *auth.SignedFlash {
	t.Helper()

	return auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
}

// postDisplayName drives HandlePlayerSetDisplayName with the given form value.
func postDisplayName(t *testing.T, store *credStubStore, displayName string) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerSetDisplayName(slog.New(slog.DiscardHandler), store, newCredFlash(t))

	form := url.Values{"display_name": {displayName}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/7/display-name",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", "7")
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: 1, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// postPassword drives HandlePlayerSetPassword with the given form value.
func postPassword(t *testing.T, store *credStubStore, password string) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerSetPassword(slog.New(slog.DiscardHandler), store, newCredFlash(t))

	form := url.Values{"password": {password}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/7/password",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", "7")
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: 1, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerSetDisplayName_SuccessRenamesAndAudits(t *testing.T) {
	t.Parallel()

	store := &credStubStore{}

	rec := postDisplayName(t, store, "  New Name  ")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if !store.renameCalled {
		t.Fatal("RenamePlayer not called, want the rename applied")
	}
	if got, want := store.renameName, "New Name"; got != want {
		t.Errorf("rename name = %q, want %q", got, want)
	}
	if got, want := store.auditAction, auth.AdminActionDisplayNameSet; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}
	if got, want := store.auditPayload, `"new_displayName":"New Name"`; !strings.Contains(got, want) {
		t.Errorf("audit payload = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerSetDisplayName_TakenFlashesNoAudit(t *testing.T) {
	t.Parallel()

	store := &credStubStore{renameErr: auth.ErrDisplayNameTaken}

	rec := postDisplayName(t, store, "taken")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.auditCalled {
		t.Error("audit written, want none when the rename collides")
	}
}

func TestHandlePlayerSetDisplayName_EmptyFlashesNoAudit(t *testing.T) {
	t.Parallel()

	store := &credStubStore{renameErr: auth.ErrDisplayNameEmpty}

	rec := postDisplayName(t, store, "   ")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.auditCalled {
		t.Error("audit written, want none on empty input")
	}
}

func TestHandlePlayerSetPassword_SuccessHashesAndAudits(t *testing.T) {
	t.Parallel()

	store := &credStubStore{}
	const plaintext = "correct horse battery staple"

	rec := postPassword(t, store, plaintext)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if !store.passCalled {
		t.Fatal("ChangePlayerPassword not called, want the password rotated")
	}
	if store.passHash == "" {
		t.Error("password hash is empty, want a bcrypt hash")
	}
	if store.passHash == plaintext {
		t.Error("password hash equals plaintext, want it hashed")
	}
	if got, want := store.auditAction, auth.AdminActionPasswordSet; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}
	if strings.Contains(store.auditPayload, plaintext) {
		t.Errorf("audit payload = %q, must not contain the plaintext password", store.auditPayload)
	}
}

func TestHandlePlayerSetPassword_TooShortRejectedNoMutation(t *testing.T) {
	t.Parallel()

	store := &credStubStore{}

	rec := postPassword(t, store, strings.Repeat("a", auth.MinPasswordLength-1))

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if store.passCalled {
		t.Error("ChangePlayerPassword called, want no mutation on a too-short password")
	}
	if store.auditCalled {
		t.Error("audit written, want none on a rejected password")
	}
}
