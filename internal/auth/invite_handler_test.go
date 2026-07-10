package auth_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

// TestHandleAcceptInviteSubmit_PreApprovesInvitedAccount pins the #1227 rule that
// an admin invite is the approval act: the invited account lands approved and
// signs in normally (not stuck at the awaiting-approval page).
func TestHandleAcceptInviteSubmit_PreApprovesInvitedAccount(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	inviter, err := stores.Players.CreatePlayer(
		t.Context(), "inviter-admin", "inviter@example.test", "h", RoleAdmin,
	)
	if err != nil {
		t.Fatalf("CreatePlayer inviter err = %v, want nil", err)
	}
	raw, hash, err := GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if err = stores.Invites.CreateInvite(
		t.Context(), "invitee@example.test", hash, "", inviter.ID, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("CreateInvite err = %v, want nil", err)
	}

	sessions := session.New([]byte("test-session-key"), false)
	deps := AcceptInviteDeps{
		Invites:  stores.Invites,
		Players:  stores.InvitePlayers,
		Sessions: sessions,
		Games:    stores.GameMigrator,
	}
	handler := HandleAcceptInviteSubmit(discardLogger(), nil, deps)
	form := url.Values{}
	form.Set("token", raw)
	form.Set("display_name", "invitee")
	form.Set("password", "new-pass-12345")
	form.Set("confirm", "new-pass-12345")
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/accept-invite", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got == "/login/pending-approval" {
		t.Errorf("Location = %q, want the role landing (invited account must sign in)", got)
	}
	invited, err := stores.Players.GetPlayerByEmail(t.Context(), "invitee@example.test")
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if !invited.IsApproved() {
		t.Error("invited account IsApproved() = false, want true (invite is the approval act)")
	}
}

func TestValidateAcceptInviteInput(t *testing.T) {
	t.Parallel()

	validPassword := strings.Repeat("a", MinPasswordLength)
	tooShort := strings.Repeat("a", MinPasswordLength-1)
	tooLong := strings.Repeat("a", MaxPasswordLength+1)

	tests := []struct {
		name        string
		displayName string
		password    string
		confirm     string
		wantMsg     string
		wantOK      bool
	}{
		{
			name:        "empty display name",
			displayName: "",
			password:    validPassword,
			confirm:     validPassword,
			wantMsg:     "Pick a display name.",
			wantOK:      false,
		},
		{
			name:        "password too short",
			displayName: "alice",
			password:    tooShort,
			confirm:     tooShort,
			wantMsg:     fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength),
			wantOK:      false,
		},
		{
			name:        "password too long",
			displayName: "alice",
			password:    tooLong,
			confirm:     tooLong,
			wantMsg:     fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLength),
			wantOK:      false,
		},
		{
			name:        "confirm mismatch",
			displayName: "alice",
			password:    validPassword,
			confirm:     validPassword + "x",
			wantMsg:     "Passwords do not match.",
			wantOK:      false,
		},
		{
			name:        "valid",
			displayName: "alice",
			password:    validPassword,
			confirm:     validPassword,
			wantMsg:     "",
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, ok := ValidateAcceptInviteInput(locale.LocaleEN, tc.displayName, tc.password, tc.confirm)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("ValidateAcceptInviteInput ok = %t, want %t", got, want)
			}
			if got, want := msg, tc.wantMsg; got != want {
				t.Errorf("ValidateAcceptInviteInput msg = %q, want %q", got, want)
			}
		})
	}
}

// TestValidateAcceptInviteInputDutch pins the Dutch translation and the
// {n} count substitution for the shared password-length message.
func TestValidateAcceptInviteInputDutch(t *testing.T) {
	t.Parallel()

	tooShort := strings.Repeat("a", MinPasswordLength-1)
	msg, ok := ValidateAcceptInviteInput(locale.LocaleNL, "alice", tooShort, tooShort)
	if got, want := ok, false; got != want {
		t.Errorf("ValidateAcceptInviteInput ok = %t, want %t", got, want)
	}
	wantMsg := fmt.Sprintf("Wachtwoord moet minstens %d tekens bevatten.", MinPasswordLength)
	if got, want := msg, wantMsg; got != want {
		t.Errorf("ValidateAcceptInviteInput msg = %q, want %q", got, want)
	}
}

func TestAcceptInviteCollisionMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantMsg string
		wantOK  bool
	}{
		{
			name:    "display name taken",
			err:     ErrDisplayNameTaken,
			wantMsg: "That display name is already taken. Pick another.",
			wantOK:  true,
		},
		{
			name:    "email taken",
			err:     ErrEmailTaken,
			wantMsg: "An account already exists for this email. Sign in instead.",
			wantOK:  true,
		},
		{
			name:    "other error",
			err:     errors.New("db down"),
			wantMsg: "",
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, ok := AcceptInviteCollisionMessage(locale.LocaleEN, tc.err)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("AcceptInviteCollisionMessage ok = %t, want %t", got, want)
			}
			if got, want := msg, tc.wantMsg; got != want {
				t.Errorf("AcceptInviteCollisionMessage msg = %q, want %q", got, want)
			}
		})
	}
}
