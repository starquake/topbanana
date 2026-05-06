// Package auth provides password hashing and verification.
package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Role values stored in the players.role column.
const (
	RolePlayer = "player"
	RoleAdmin  = "admin"
)

// HashPassword returns the bcrypt hash of the given plaintext password.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	return string(h), nil
}

// CheckPassword reports whether the given plaintext password matches the hash.
// It wraps [bcrypt.ErrMismatchedHashAndPassword] when the password is wrong; use [errors.Is] to check.
func CheckPassword(hashed, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)); err != nil {
		return fmt.Errorf("checking password: %w", err)
	}

	return nil
}
