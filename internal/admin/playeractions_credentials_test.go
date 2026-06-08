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

func newCredFlash(t *testing.T) *auth.SignedFlash {
	t.Helper()

	return auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
}

// auditEntries returns the admin-audit rows recorded against the target
// player, newest first, so a test can assert what (if anything) the
// handler audited.
func (e *adminEnv) auditEntries(t *testing.T, targetID int64) []*auth.AdminAuditEntry {
	t.Helper()

	entries, err := e.admin.ListAdminAuditForTarget(t.Context(), targetID, 10)
	if err != nil {
		t.Fatalf("ListAdminAuditForTarget(%d) err = %v, want nil", targetID, err)
	}

	return entries
}

// postDisplayName drives HandlePlayerSetDisplayName against the target
// player with the given form value.
func postDisplayName(
	t *testing.T, env *adminEnv, targetID int64, displayName string,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerSetDisplayName(slog.New(slog.DiscardHandler), env.admin, newCredFlash(t))

	form := url.Values{"display_name": {displayName}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(targetID, 10)+"/display-name",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(targetID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// postPassword drives HandlePlayerSetPassword against the target player
// with the given form value.
func postPassword(
	t *testing.T, env *adminEnv, targetID int64, password string,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerSetPassword(slog.New(slog.DiscardHandler), env.admin, newCredFlash(t))

	form := url.Values{"password": {password}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(targetID, 10)+"/password",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(targetID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerSetDisplayName_SuccessRenamesAndAudits(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedPlayer(t, "before")

	rec := postDisplayName(t, env, target, "  New Name  ")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// The rename trims and persists; reload to confirm.
	detail, err := env.admin.GetPlayerDetail(t.Context(), target)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := detail.DisplayName, "New Name"; got != want {
		t.Errorf("display name = %q, want %q", got, want)
	}
	entries := env.auditEntries(t, target)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("audit entries = %d, want %d", got, want)
	}
	if got, want := entries[0].Action, auth.AdminActionDisplayNameSet; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}
	if got, want := entries[0].Payload, `"new_displayName":"New Name"`; !strings.Contains(got, want) {
		t.Errorf("audit payload = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerSetDisplayName_TakenFlashesNoAudit(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Two players; renaming the target to the other's name collides on the
	// UNIQUE display_name index, producing auth.ErrDisplayNameTaken.
	env.seedPlayer(t, "taken")
	target := env.seedPlayer(t, "before")

	rec := postDisplayName(t, env, target, "taken")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d when the rename collides", got, want)
	}
	// The name must be unchanged.
	detail, err := env.admin.GetPlayerDetail(t.Context(), target)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got, want := detail.DisplayName, "before"; got != want {
		t.Errorf("display name = %q, want %q (unchanged)", got, want)
	}
}

func TestHandlePlayerSetDisplayName_EmptyFlashesNoAudit(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedPlayer(t, "before")

	// A whitespace-only value trims to "" and produces ErrDisplayNameEmpty.
	rec := postDisplayName(t, env, target, "   ")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on empty input", got, want)
	}
}

func TestHandlePlayerSetPassword_SuccessHashesAndAudits(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// The target carries an email (OAuth-only, no password) so the
	// CHECK (password_hash IS NULL OR email IS NOT NULL) constraint is
	// satisfied once the admin sets a password.
	target := env.seedOAuthPlayer(t, "before", "before@example.test", "google", "sub-before")
	const plaintext = "correct horse battery staple"

	rec := postPassword(t, env, target, plaintext)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// The target now carries a password.
	detail, err := env.admin.GetPlayerDetail(t.Context(), target)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if !detail.HasPassword {
		t.Error("HasPassword = false, want the password rotated")
	}
	entries := env.auditEntries(t, target)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("audit entries = %d, want %d", got, want)
	}
	if got, want := entries[0].Action, auth.AdminActionPasswordSet; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}
	if strings.Contains(entries[0].Payload, plaintext) {
		t.Errorf("audit payload = %q, must not contain the plaintext password", entries[0].Payload)
	}
}

func TestHandlePlayerSetPassword_TooShortRejectedNoMutation(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedOAuthPlayer(t, "before", "before@example.test", "google", "sub-before")

	rec := postPassword(t, env, target, strings.Repeat("a", auth.MinPasswordLength-1))

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	detail, err := env.admin.GetPlayerDetail(t.Context(), target)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if detail.HasPassword {
		t.Error("HasPassword = true, want no mutation on a too-short password")
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on a rejected password", got, want)
	}
}

func TestHandlePlayerSetEmail_OversizedFormRejectedWith400(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedPlayer(t, "before")

	handler := HandlePlayerSetEmail(slog.New(slog.DiscardHandler), env.admin, newCredFlash(t))

	// A well-formed email body that MaxFormSizeMiddleware caps before the
	// handler reads it. Wrapping the body in a tiny MaxBytesReader trips
	// ParseForm the same way an oversized submission does, so the values
	// come back empty and the handler must answer 400 rather than the
	// misleading "Enter a valid email address." validation flash.
	form := url.Values{"email": {"new@example.test"}}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(target, 10)+"/email",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(target, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, 1)
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Enter a valid email address."; strings.Contains(got, want) {
		t.Errorf("body = %q, must not contain the validation message %q", got, want)
	}
	// The oversized request must not mutate the target or audit anything.
	detail, err := env.admin.GetPlayerDetail(t.Context(), target)
	if err != nil {
		t.Fatalf("GetPlayerDetail err = %v, want nil", err)
	}
	if got := detail.Email; got != "" {
		t.Errorf("email = %q, want empty (no mutation on a rejected form)", got)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on a rejected form", got, want)
	}
}

// newPlayerInputRequest builds a POST request carrying the create-player
// form fields as a urlencoded body. httptest.NewRequest is banned by the
// noctx linter, so the request is built with a context.
func newPlayerInputRequest(t *testing.T, displayName, email, password string) *http.Request {
	t.Helper()

	form := url.Values{}
	form.Set("display_name", displayName)
	form.Set("email", email)
	form.Set("password", password)
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

// postCreatePlayer drives HandlePlayerCreateSubmit with the given form
// fields, posing as the seeded admin so requireAdminActor passes.
func postCreatePlayer(
	t *testing.T, env *adminEnv, displayName, email, password string,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerCreateSubmit(slog.New(slog.DiscardHandler), nil, env.admin, newCredFlash(t))

	req := newPlayerInputRequest(t, displayName, email, password)
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerCreateSubmit_DisplayNameTakenRenders409(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Seed a player owning the display name; the admin-create then collides
	// on the UNIQUE display_name index, mapping to auth.ErrDisplayNameTaken.
	env.seedCredentialledPlayer(t, "Taken Name", "existing@example.test", auth.RolePlayer)

	rec := postCreatePlayer(t, env, "Taken Name", "fresh@example.test", "correct-horse-battery")

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "That display name is already taken."; !strings.Contains(got, want) {
		t.Errorf("body should contain %q; body=%q", want, got)
	}
}

func TestHandlePlayerCreateSubmit_EmailTakenRenders409(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Seed a player owning the email; the admin-create uses a fresh display
	// name so the only collision is the UNIQUE email index, mapping to
	// auth.ErrEmailTaken.
	env.seedCredentialledPlayer(t, "Existing Name", "taken@example.test", auth.RolePlayer)

	rec := postCreatePlayer(t, env, "Fresh Name", "taken@example.test", "correct-horse-battery")

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Another account already uses that email."; !strings.Contains(got, want) {
		t.Errorf("body should contain %q; body=%q", want, got)
	}
}

func TestNewPlayerInput(t *testing.T) {
	t.Parallel()

	const (
		validEmail = "new@example.test"
		// MinPasswordLength is 13, MaxPasswordLength is 72.
		validPassword = "correct-horse-battery"
	)

	t.Run("blank display name falls back to a generated petname", func(t *testing.T) {
		t.Parallel()

		got := NewPlayerInputResultFor(newPlayerInputRequest(t, "", validEmail, validPassword))
		if got.DisplayName == "" {
			t.Error("DisplayName = empty, want a generated petname")
		}
		if got.ErrMsg != "" {
			t.Errorf("ErrMsg = %q, want empty", got.ErrMsg)
		}
	})

	t.Run("invalid email is rejected", func(t *testing.T) {
		t.Parallel()

		got := NewPlayerInputResultFor(newPlayerInputRequest(t, "Alice", "not-an-email", validPassword))
		if want := "Enter a valid email address."; got.ErrMsg != want {
			t.Errorf("ErrMsg = %q, want %q", got.ErrMsg, want)
		}
	})

	t.Run("too-short password is rejected", func(t *testing.T) {
		t.Parallel()

		got := NewPlayerInputResultFor(newPlayerInputRequest(t, "Alice", validEmail, "short"))
		if want := "at least"; !strings.Contains(got.ErrMsg, want) {
			t.Errorf("ErrMsg = %q, should contain %q", got.ErrMsg, want)
		}
	})

	t.Run("too-long password is rejected", func(t *testing.T) {
		t.Parallel()

		got := NewPlayerInputResultFor(
			newPlayerInputRequest(t, "Alice", validEmail, strings.Repeat("x", 73)),
		)
		if want := "at most"; !strings.Contains(got.ErrMsg, want) {
			t.Errorf("ErrMsg = %q, should contain %q", got.ErrMsg, want)
		}
	})

	t.Run("valid input has no error message", func(t *testing.T) {
		t.Parallel()

		got := NewPlayerInputResultFor(newPlayerInputRequest(t, "Alice", validEmail, validPassword))
		if got.ErrMsg != "" {
			t.Errorf("ErrMsg = %q, want empty", got.ErrMsg)
		}
		if got.DisplayName != "Alice" {
			t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Alice")
		}
	})
}
