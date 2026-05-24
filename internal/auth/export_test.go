package auth

import "context"

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

// ExportVerifySignedState exposes verifySignedState so tests can pin
// the state-cookie HMAC round-trip from string inputs, exercising
// exactly the production validation path.
func ExportVerifySignedState(key []byte, cookieValue, queryValue string) error {
	return verifySignedState(key, cookieValue, queryValue)
}
