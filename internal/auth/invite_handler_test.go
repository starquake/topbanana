package auth_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
)

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

			msg, ok := ValidateAcceptInviteInput(tc.displayName, tc.password, tc.confirm)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("ValidateAcceptInviteInput ok = %t, want %t", got, want)
			}
			if got, want := msg, tc.wantMsg; got != want {
				t.Errorf("ValidateAcceptInviteInput msg = %q, want %q", got, want)
			}
		})
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
			wantMsg: "An account already exists for this email - sign in instead.",
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

			msg, ok := AcceptInviteCollisionMessage(tc.err)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("AcceptInviteCollisionMessage ok = %t, want %t", got, want)
			}
			if got, want := msg, tc.wantMsg; got != want {
				t.Errorf("AcceptInviteCollisionMessage msg = %q, want %q", got, want)
			}
		})
	}
}
