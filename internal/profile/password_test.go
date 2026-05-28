package profile_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	. "github.com/starquake/topbanana/internal/profile"
)

func TestValidatePasswordChangeInput_AcceptsWellFormed(t *testing.T) {
	t.Parallel()

	password := strings.Repeat("a", auth.MinPasswordLength)
	msg, ok := ValidatePasswordChangeInput(password, password)
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
	msg, ok := ValidatePasswordChangeInput(short, short)
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
	msg, ok := ValidatePasswordChangeInput(long, long)
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
	msg, ok := ValidatePasswordChangeInput(password, confirm)
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
	msg, ok := ValidatePasswordChangeInput(short, mismatch)
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
