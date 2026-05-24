package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// ExportLinkOrCreateGooglePlayer is the test-only alias for the
// unexported find-or-link decision used by HandleGoogleCallback. Lets
// google_test.go unit-test the branch table directly without booting a
// full OAuth dance.
func ExportLinkOrCreateGooglePlayer(
	ctx context.Context,
	store OAuthIdentityStore,
	subject, email string,
) (*Player, error) {
	return linkOrCreateGooglePlayer(ctx, store, subject, email)
}

// ExportSignState exposes signState so tests can assert the cookie
// format round-trips correctly without re-deriving the HMAC layout in
// the test code.
func ExportSignState(key []byte, nonce string) string {
	return signState(key, nonce)
}

// ExportValidateStateValues runs the same checks validateState does
// against caller-supplied cookie + query values, sidestepping the
// http.Request plumbing tests would otherwise have to build.
func ExportValidateStateValues(key []byte, cookieValue, queryValue string) error {
	if cookieValue == "" || queryValue == "" {
		return ErrGoogleStateMismatch
	}
	if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(queryValue)) != 1 {
		return ErrGoogleStateMismatch
	}
	parts, ok := splitState(cookieValue)
	if !ok {
		return ErrGoogleStateMismatch
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(parts.Nonce))
	wantMAC := h.Sum(nil)
	gotMAC, err := base64.RawURLEncoding.DecodeString(parts.MAC)
	if err != nil {
		return ErrGoogleStateMismatch
	}
	if !hmac.Equal(gotMAC, wantMAC) {
		return ErrGoogleStateMismatch
	}

	return nil
}
