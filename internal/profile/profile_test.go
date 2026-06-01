package profile_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	. "github.com/starquake/topbanana/internal/profile"
)

// renameStubStore implements auth.PlayerStore for the
// HandleProfileDisplayName tests. Only RenamePlayer does anything; the
// rest return a sentinel so an accidental call is loud.
type renameStubStore struct {
	renameErr error
}

func (s *renameStubStore) RenamePlayer(_ context.Context, _ int64, displayName string) (*auth.Player, error) {
	if s.renameErr != nil {
		return nil, s.renameErr
	}

	return &auth.Player{ID: 7, DisplayName: displayName}, nil
}

func (*renameStubStore) GetPlayerByDisplayName(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) GetPlayerByEmail(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) GetPlayerByID(_ context.Context, _ int64) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) CreatePlayer(
	_ context.Context, _, _, _, _ string,
) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) CreateAnonymousPlayer(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) ClaimPlayer(
	_ context.Context, _ int64, _, _, _, _ string,
) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*renameStubStore) SetPlayerPasswordHash(_ context.Context, _, _ string) error {
	return errors.ErrUnsupported
}

func (*renameStubStore) ChangePlayerPassword(_ context.Context, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*renameStubStore) UpdatePlayerDisplayName(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

// postRename drives HandleProfileDisplayName with the given store and form
// value, returning the captured log output and the response recorder.
func postRename(t *testing.T, store auth.PlayerStore, newName string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	csrfMgr := csrf.New([]byte("test-key-32-bytes-test-key-32byt"), false)
	handler := HandleProfileDisplayName(logger, csrfMgr, store)

	form := url.Values{"display_name": {newName}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/profile/display-name",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: 7, DisplayName: "current-name"}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return logs.String(), rec
}

func TestHandleProfileDisplayName_LogsTakenRejection(t *testing.T) {
	t.Parallel()

	logs, rec := postRename(t, &renameStubStore{renameErr: auth.ErrDisplayNameTaken}, "taken-name")

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := logs, "rename rejected: name taken"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	// The attempted name is logged so the collision target is visible.
	if got, want := logs, "taken-name"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain attempted name %q", got, want)
	}
}

func TestHandleProfileDisplayName_LogsEmptyRejection(t *testing.T) {
	t.Parallel()

	logs, rec := postRename(t, &renameStubStore{renameErr: auth.ErrDisplayNameEmpty}, "ignored")

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := logs, "rename rejected: empty name"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
}

func TestHandleProfileDisplayName_LogsUnexpectedErrorAtError(t *testing.T) {
	t.Parallel()

	logs, rec := postRename(t, &renameStubStore{renameErr: errors.New("db exploded")}, "any-name")

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := logs, "profile rename failed"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	if got, want := logs, "level=ERROR"; !strings.Contains(got, want) {
		t.Errorf("log = %q, unexpected error should log at ERROR", got)
	}
}
