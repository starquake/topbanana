package auth

import "context"

// Identity provider names stored in player_identities.provider. Sentinel
// strings instead of an iota so the values are stable when read straight
// from the database in ad-hoc queries.
const (
	ProviderGoogle = "google"
)

// OAuthIdentityStore is the persistence interface used by the OAuth
// sign-in handlers. Kept separate from PlayerStore because the OAuth
// flow only needs identity lookups + a credential-less create path; a
// future provider (GitHub, Apple) can reuse this interface unchanged.
type OAuthIdentityStore interface {
	// GetPlayerByProviderSubject returns the player whose
	// player_identities row matches (provider, subject). Returns
	// ErrPlayerNotFound when no identity is linked yet.
	GetPlayerByProviderSubject(ctx context.Context, provider, subject string) (*Player, error)
	// GetPlayerByEmail returns the player whose players.email matches.
	// Used by the OAuth callback to silently link a fresh identity onto
	// an existing row when the verified email already belongs to a
	// player. Returns ErrPlayerNotFound when no row matches.
	GetPlayerByEmail(ctx context.Context, email string) (*Player, error)
	// CreatePlayerFromOAuth inserts a new players row for a first-time
	// OAuth sign-in and links the (provider, subject) identity onto it in
	// one transaction. password_hash is left NULL, email is the supplied
	// verified address, and displayName is the caller-generated petname.
	// Returns ErrDisplayNameTaken if the petname collides (the OAuth
	// handler retries with a fresh petname on this sentinel) and
	// ErrIdentityAlreadyLinked if the identity is already linked.
	CreatePlayerFromOAuth(ctx context.Context, displayName, email, provider, subject string) (*Player, error)
	// LinkProviderIdentity attaches a (provider, subject) pair to the
	// given player id and treats the row's email as provider-proven in
	// the same transaction: an unverified row is stamped verified, its
	// unproven password dropped, and its sessions invalidated. Returns
	// the row as it stands afterwards, or ErrIdentityAlreadyLinked when
	// the (provider, subject) pair already exists (UNIQUE collision).
	LinkProviderIdentity(ctx context.Context, playerID int64, provider, subject string) (*Player, error)
	// ClaimPlayerForOAuth attaches the supplied verified email to an
	// existing anonymous (no password_hash, no email) players row and
	// links the (provider, subject) identity onto it in one transaction,
	// so a visitor's pre-sign-in identity carries onto their first OAuth
	// login. Returns ErrPlayerNotFound when the row is missing, has
	// already been credentialled, or already carries an email - in
	// each case the caller falls through to the create-fresh-player
	// path - and ErrIdentityAlreadyLinked when the identity is already
	// linked elsewhere (the claim is rolled back). The displayName on the
	// row is left untouched.
	ClaimPlayerForOAuth(ctx context.Context, playerID int64, email, provider, subject string) (*Player, error)
	// MarkPlayerEmailVerifiedByOAuth applies the provider-attested proof
	// LinkProviderIdentity applies, for a row whose identity is already
	// linked. Returns the row as it stands afterwards.
	MarkPlayerEmailVerifiedByOAuth(ctx context.Context, playerID int64) (*Player, error)
}
