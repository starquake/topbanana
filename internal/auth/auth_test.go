package auth_test

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	. "github.com/starquake/topbanana/internal/auth"
)

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()

	plain := "correct horse battery staple"
	hashed, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword(%q) err = %v, want nil", plain, err)
	}
	if hashed == plain {
		t.Errorf("HashPassword(%q) = %q, want a value different from the plaintext", plain, hashed)
	}
	if err := CheckPassword(hashed, plain); err != nil {
		t.Errorf("CheckPassword(_, %q) = %v, want nil", plain, err)
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	t.Parallel()

	hashed, err := HashPassword("right")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	checkErr := CheckPassword(hashed, "wrong")
	if got, want := checkErr, bcrypt.ErrMismatchedHashAndPassword; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
	if got, want := checkErr.Error(), "checking password"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

func TestCheckPassword_MalformedHash(t *testing.T) {
	t.Parallel()

	if got, want := CheckPassword("not-a-bcrypt-hash", "anything"), bcrypt.ErrHashTooShort; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}
