package auth

import (
	"context"
	"net/http"
)

// ExportLinkOrCreateGooglePlayer is the test-only alias for the
// unexported find-or-link decision used by HandleGoogleCallback. Lets
// google_test.go unit-test the branch table directly without booting a
// full OAuth dance.
func ExportLinkOrCreateGooglePlayer(
	ctx context.Context,
	store OAuthIdentityStore,
	subject, email string,
	sessionPlayerID *int64,
	registrationEnabled bool,
) (*Player, error) {
	return linkOrCreateGooglePlayer(ctx, store, subject, email, sessionPlayerID, registrationEnabled)
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

// ExportClaimAnonymousSessionPlayer exposes claimAnonymousSessionPlayer
// so the race-recovery branch (ClaimPlayerForOAuth returns
// ErrPlayerNotFound but the identity is already linked elsewhere) can
// be unit-tested with a stub store, without staging the whole
// linkOrCreateGooglePlayer flow.
func ExportClaimAnonymousSessionPlayer(
	ctx context.Context,
	identities OAuthIdentityStore,
	sessionPlayerID int64,
	subject, email string,
) (*Player, error) {
	return claimAnonymousSessionPlayer(ctx, identities, sessionPlayerID, subject, email)
}

// ExportLinkExistingPlayerByEmail exposes linkExistingPlayerByEmail so
// the silent link-by-email branches (lookup error, link-race refetch,
// mark-verified error) can be unit-tested with a fault-injection store
// without staging the whole linkOrCreateGooglePlayer flow.
func ExportLinkExistingPlayerByEmail(
	ctx context.Context,
	identities OAuthIdentityStore,
	subject, email string,
) (*Player, error) {
	return linkExistingPlayerByEmail(ctx, identities, subject, email)
}

// ExportCreateGooglePlayer exposes createGooglePlayer so the
// race-recovery branch (LinkProviderIdentity returns
// ErrIdentityAlreadyLinked because another callback won) can be
// unit-tested with a stub store.
func ExportCreateGooglePlayer(
	ctx context.Context,
	identities OAuthIdentityStore,
	subject, email string,
) (*Player, error) {
	return createGooglePlayer(ctx, identities, subject, email)
}

// ExportSignNext exposes signNext so the OAuth next-cookie round-trip
// can be asserted from string inputs (#449).
func ExportSignNext(key []byte, path string) string {
	return signNext(key, path)
}

// ExportReadGoogleNext exposes readGoogleNext so the OAuth next-cookie
// round-trip can be asserted end-to-end from a built request.
func ExportReadGoogleNext(r *http.Request, key []byte) string {
	return readGoogleNext(r, key)
}

// GoogleNextCookieName exposes the OAuth next-cookie name so
// integration / unit tests can assert it without re-declaring the
// constant.
const GoogleNextCookieName = googleNextCookieName

// BuildVerifyLink exposes buildVerifyLink so the URL-shape tests can
// assert the link path + query encoding from the external test package.
var BuildVerifyLink = buildVerifyLink

// ErrVerifyBaseURLEmpty / ErrVerifyBaseURLInvalid expose the sentinel
// errors buildVerifyLink returns so the external test package can match
// on them via [errors.Is].
var (
	ErrVerifyBaseURLEmpty   = errVerifyBaseURLEmpty
	ErrVerifyBaseURLInvalid = errVerifyBaseURLInvalid
)

// NewLoginRateLimiterWithClock exposes the internal clock-injected
// rate-limiter constructor so the external auth_test package can pin
// the per-IP cool-down without sleeping (#494).
var NewLoginRateLimiterWithClock = newLoginRateLimiterWithClock

// NewAccountLoginLimiterWithClock exposes the internal clock-injected
// per-account limiter constructor so the external auth_test package can
// pin the cooldown expiry and prune without sleeping (#786).
var NewAccountLoginLimiterWithClock = newAccountLoginLimiterWithClock

// ValidateAcceptInviteInput exposes validateAcceptInviteInput so the
// external test package can pin the display-name + password rule table
// from input strings without staging an HTTP request.
var ValidateAcceptInviteInput = validateAcceptInviteInput

// AcceptInviteCollisionMessage exposes acceptInviteCollisionMessage so
// the external test package can pin the create-conflict sentinel
// mapping without staging the whole accept-invite flow.
var AcceptInviteCollisionMessage = acceptInviteCollisionMessage
