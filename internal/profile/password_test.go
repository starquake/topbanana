package profile_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/locale"
	. "github.com/starquake/topbanana/internal/profile"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestValidatePasswordChangeInput_AcceptsWellFormed(t *testing.T) {
	t.Parallel()

	password := strings.Repeat("a", auth.MinPasswordLength)
	msg, ok := ValidatePasswordChangeInput(locale.LocaleEN, password, password)
	if !ok {
		t.Errorf("ValidatePasswordChangeInput(<%d a's>, <%d a's>) ok = false, want true (msg=%q)",
			auth.MinPasswordLength, auth.MinPasswordLength, msg)
	}
	if msg != "" {
		t.Errorf("ValidatePasswordChangeInput(<min-length>, <min-length>) msg = %q, want empty", msg)
	}
}

func TestValidatePasswordChangeInput_RejectsTooShort(t *testing.T) {
	t.Parallel()

	short := strings.Repeat("a", auth.MinPasswordLength-1)
	msg, ok := ValidatePasswordChangeInput(locale.LocaleEN, short, short)
	if ok {
		t.Errorf("ValidatePasswordChangeInput(<%d a's>, <%d a's>) ok = true, want false",
			auth.MinPasswordLength-1, auth.MinPasswordLength-1)
	}
	wantMsg := fmt.Sprintf("Password must be at least %d characters.", auth.MinPasswordLength)
	if got, want := msg, wantMsg; got != want {
		t.Errorf("ValidatePasswordChangeInput(<too-short>, <too-short>) msg = %q, want %q", got, want)
	}
}

func TestValidatePasswordChangeInput_RejectsTooLong(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", auth.MaxPasswordLength+1)
	msg, ok := ValidatePasswordChangeInput(locale.LocaleEN, long, long)
	if ok {
		t.Errorf("ValidatePasswordChangeInput(<%d a's>, <%d a's>) ok = true, want false",
			auth.MaxPasswordLength+1, auth.MaxPasswordLength+1)
	}
	wantMsg := fmt.Sprintf("Password must be at most %d characters.", auth.MaxPasswordLength)
	if got, want := msg, wantMsg; got != want {
		t.Errorf("ValidatePasswordChangeInput(<too-long>, <too-long>) msg = %q, want %q", got, want)
	}
}

func TestValidatePasswordChangeInput_RejectsMismatchedConfirm(t *testing.T) {
	t.Parallel()

	password := strings.Repeat("a", auth.MinPasswordLength)
	confirm := strings.Repeat("b", auth.MinPasswordLength)
	msg, ok := ValidatePasswordChangeInput(locale.LocaleEN, password, confirm)
	if ok {
		t.Errorf("ValidatePasswordChangeInput(%q, %q) ok = true, want false", password, confirm)
	}
	if got, want := msg, "Passwords do not match."; got != want {
		t.Errorf("ValidatePasswordChangeInput(<mismatch>) msg = %q, want %q", got, want)
	}
}

func TestValidatePasswordChangeInput_LengthBeforeMismatch(t *testing.T) {
	t.Parallel()

	short := strings.Repeat("a", auth.MinPasswordLength-1)
	mismatch := strings.Repeat("b", auth.MinPasswordLength)
	msg, ok := ValidatePasswordChangeInput(locale.LocaleEN, short, mismatch)
	if ok {
		t.Errorf("ValidatePasswordChangeInput(%q, %q) ok = true, want false", short, mismatch)
	}
	if got, want := msg, "Password must be at least"; !strings.Contains(got, want) {
		t.Errorf(
			"ValidatePasswordChangeInput(<short>, <ok-length-but-mismatched>) msg = %q, should contain %q (length checked before mismatch)",
			got,
			want,
		)
	}
}

// passwordChangeCurrent is the current password the in-context player is
// seeded with; the form submits it so the current-password gate passes
// and the rotation branches under test are reached.
const passwordChangeCurrent = "correct-battery-13"

// passwordChangeResult bundles what postPasswordChange surfaces.
type passwordChangeResult struct {
	logs string
	rec  *httptest.ResponseRecorder
}

// postPasswordChange drives HandleProfilePasswordChange with the given
// store. The in-context player carries id 7 and a hash of
// passwordChangeCurrent, and the form submits a fresh, well-formed new
// password so validation and the current-password check both pass,
// isolating the rotation / reload error branches.
func postPasswordChange(t *testing.T, players auth.PlayerStore) passwordChangeResult {
	t.Helper()

	hash, err := auth.HashPassword(passwordChangeCurrent)
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}

	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	csrfMgr := csrf.New([]byte("test-key-32-bytes-test-key-32byt"), false)
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), false)
	handler := HandleProfilePasswordChange(logger, csrfMgr, players, sessions)

	newPassword := strings.Repeat("z", auth.MinPasswordLength)
	form := url.Values{
		"current_password":     {passwordChangeCurrent},
		"new_password":         {newPassword},
		"new_password_confirm": {newPassword},
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/profile/password", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: 7, PasswordHash: hash}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return passwordChangeResult{logs: logs.String(), rec: rec}
}

// sessionRefreshed reports whether the response set a non-clearing
// session cookie, which Manager.Set does only when the rotation +
// reload both succeed.
func sessionRefreshed(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value != "" && c.MaxAge >= 0 {
			return true
		}
	}

	return false
}

// TestHandleProfilePasswordChange_PlayerVanishedDuringRotate drives a real
// PlayerStore whose row for the in-context player id never existed, so
// ChangePlayerPassword reports ErrPlayerNotFound (the "player vanished
// mid-request" branch) -- the real rows==0 path, not an injected sentinel.
func TestHandleProfilePasswordChange_PlayerVanishedDuringRotate(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), slog.Default())

	res := postPasswordChange(t, players)

	if got, want := res.rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.logs, "change password: player vanished mid-request"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	if sessionRefreshed(res.rec) {
		t.Error("session cookie was refreshed, want it left untouched on rotation failure")
	}
}

// TestHandleProfilePasswordChange_GenericRotateError drives a real
// PlayerStore over a closed DB so ChangePlayerPassword fails with a
// wrapped driver error (the generic rotate-error branch), per the
// project rule to prefer a closed DB over a double for an ordinary
// store-errored branch.
func TestHandleProfilePasswordChange_GenericRotateError(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	players := store.NewPlayerStore(db, slog.Default())
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	res := postPasswordChange(t, players)

	if got, want := res.rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.logs, "error rotating password"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	if sessionRefreshed(res.rec) {
		t.Error("session cookie was refreshed, want it left untouched on rotation failure")
	}
}

// reloadFailStore is a PlayerStore whose rotate succeeds but whose
// post-rotate reload fails. A real store cannot reproduce this: the row
// ChangePlayerPassword just updated is still present for GetPlayerByID to
// read back, so the reload failure has to be injected. Every other method
// returns a sentinel so an accidental call surfaces loudly.
type reloadFailStore struct{ getErr error }

func (*reloadFailStore) ChangePlayerPassword(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *reloadFailStore) GetPlayerByID(_ context.Context, _ int64) (*auth.Player, error) {
	return nil, s.getErr
}

func (*reloadFailStore) GetPlayerByDisplayName(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) GetPlayerByEmail(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) CreatePlayer(_ context.Context, _, _, _, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) CreateAnonymousPlayer(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) ClaimPlayer(_ context.Context, _ int64, _, _, _, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) RenamePlayer(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

func (*reloadFailStore) SetPlayerPasswordHash(_ context.Context, _, _ string) error {
	return errors.ErrUnsupported
}

func (*reloadFailStore) UpdatePlayerDisplayName(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errors.ErrUnsupported
}

// TestHandleProfilePasswordChange_ReloadAfterRotateFails pins the one
// branch a real store cannot reproduce: the rotation commits but the
// reload that follows it errors, so the handler 500s without refreshing
// the session.
func TestHandleProfilePasswordChange_ReloadAfterRotateFails(t *testing.T) {
	t.Parallel()

	res := postPasswordChange(t, &reloadFailStore{getErr: errors.New("reload failed")})

	if got, want := res.rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.logs, "error reloading player after password change"; !strings.Contains(got, want) {
		t.Errorf("log = %q, should contain %q", got, want)
	}
	if sessionRefreshed(res.rec) {
		t.Error("session cookie was refreshed, want it left untouched on reload failure")
	}
}
