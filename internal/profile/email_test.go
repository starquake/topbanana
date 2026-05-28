package profile_test

import (
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/profile"
)

func TestValidateEmailChange_Cases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		newEmail     string
		currentEmail string
		wantOK       bool
		wantMsgPart  string
	}{
		{
			name:         "blank rejected",
			newEmail:     "",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "Enter a new email",
		},
		{
			name:         "malformed rejected",
			newEmail:     "not-an-email",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "valid email",
		},
		{
			name:         "matches current rejected",
			newEmail:     "current@example.test",
			currentEmail: "current@example.test",
			wantOK:       false,
			wantMsgPart:  "already your address",
		},
		{
			name:         "matches current case-insensitive rejected",
			newEmail:     "current@example.test",
			currentEmail: "CURRENT@Example.Test",
			wantOK:       false,
			wantMsgPart:  "already your address",
		},
		{
			name:         "fresh value accepted",
			newEmail:     "fresh@example.test",
			currentEmail: "current@example.test",
			wantOK:       true,
			wantMsgPart:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, ok := ExportValidateEmailChange(tc.newEmail, tc.currentEmail)
			if got, want := ok, tc.wantOK; got != want {
				t.Errorf("ok = %v, want %v (msg=%q)", got, want, msg)
			}
			if tc.wantMsgPart != "" {
				if got, want := msg, tc.wantMsgPart; !strings.Contains(got, want) {
					t.Errorf("msg = %q, should contain %q", got, want)
				}
			}
		})
	}
}
