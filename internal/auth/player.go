package auth

import (
	"context"
	"errors"
	"time"
)

// ErrPlayerNotFound is returned when a player is not found by username.
var ErrPlayerNotFound = errors.New("player not found")

// ErrUsernameTaken is returned when a username is already in use.
var ErrUsernameTaken = errors.New("username taken")

// ErrEmailTaken is returned when an email is already in use.
var ErrEmailTaken = errors.New("email taken")

// ErrPlayerAlreadyClaimed is returned by ClaimPlayer when the target row
// already has a password_hash set, so it cannot be upgraded again.
var ErrPlayerAlreadyClaimed = errors.New("player already claimed")

// ErrPlayerNotAnonymous is returned by UpdatePlayerUsername when the target
// row exists but already carries a non-anonymous identity (password_hash IS
// NOT NULL). Username-only changes are reserved for anonymous rows; a
// credentialled account must change its name through a different flow.
var ErrPlayerNotAnonymous = errors.New("player is not anonymous")

// ErrUsernameEmpty is returned by UpdatePlayerUsername when the supplied
// username trims to the empty string. Surfaced as a store-level sentinel
// so callers can map it to a 400 without re-validating themselves.
var ErrUsernameEmpty = errors.New("username is empty")

// ErrIdentityAlreadyLinked is returned by LinkProviderIdentity when the
// (provider, subject) pair already exists. Distinct from ErrUsernameTaken
// so the Google callback can distinguish a race between two concurrent
// first-time sign-ins (handled by retrying the lookup) from a true
// collision (handled as a 500).
var ErrIdentityAlreadyLinked = errors.New("identity already linked")

// Player represents an authenticated user (admin or player).
type Player struct {
	ID       int64
	Username string
	Email    string
	// EmailVerifiedAt is nil until the address is verified. OAuth-linked
	// rows are stamped at link time because the provider attests the email.
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Role            string
	CreatedAt       time.Time
	UsernameClaimed bool
}

// IsEmailVerified reports whether the player's email has been verified.
func (p *Player) IsEmailVerified() bool {
	return p.EmailVerifiedAt != nil
}

// IsAnonymous reports whether the player has no credentials set yet (no
// password_hash) AND is not an admin. Anonymous rows are created by
// EnsurePlayer for visitors who arrive without a session and may later be
// upgraded by ClaimPlayer.
//
// The admin-role exclusion guards the seeded admin row (id=1), which also
// has a NULL password_hash but must never be treated as a claimable
// anonymous row by HandleRegisterSubmit's claim path.
func (p *Player) IsAnonymous() bool {
	return p.PasswordHash == "" && p.Role != RoleAdmin
}

// HasCustomName reports whether the player has explicitly picked their
// username. Anonymous rows start with HasCustomName=false (the username
// is an auto-generated petname); they flip to true on a successful
// PATCH /api/players/me or via the register flow. Distinct from
// IsAnonymous() (which is credential-level: no password): a
// claimed-but-passwordless visitor has HasCustomName=true and
// IsAnonymous()=true simultaneously.
func (p *Player) HasCustomName() bool {
	return p.UsernameClaimed
}

// IsAuthenticated reports whether the visitor has signed in. True for
// password rows, OAuth-linked rows, and the seeded admin. Distinct
// from [Player.IsEmailVerified] - the email-verify gate is a separate
// predicate so an unverified password row can still be session-bearing.
func (p *Player) IsAuthenticated() bool {
	return p.PasswordHash != "" || p.Email != "" || p.Role == RoleAdmin
}

// AnonymousGameMigrator carries an anonymous visitor's game data onto
// the account they just signed into. Implemented by store.GameStore;
// defined here so the auth package can call into it without importing
// internal/game or internal/store, keeping the dep graph narrow and
// the post-login migration testable with a small stub.
//
// ReattributeGames moves every game_answers + game_participants row
// from fromPlayerID onto toPlayerID, skipping quizzes the destination
// player has already played (UNIQUE (player_id, quiz_id) on
// game_participants). Both moves run in a single transaction; the
// return value is the number of participant rows that actually
// moved (zero is a valid "nothing to do" outcome).
type AnonymousGameMigrator interface {
	ReattributeGames(ctx context.Context, fromPlayerID, toPlayerID int64) (int64, error)
}

// PlayerListRow is one row in the admin players list (#423). Mirrors
// the shape of the underlying SQL more closely than auth.Player —
// adds the derived OAuth-link state so the handler does not have to
// re-derive it per row. FinishedCount and LastFinishedAt come from a
// separate PlayerStats lookup so the page query stays simple.
type PlayerListRow struct {
	ID            int64
	Username      string
	Email         string
	Role          string
	HasPassword   bool
	HasOAuth      bool
	OAuthProvider string
	CreatedAt     time.Time
}

// PlayerStats is the per-player finished-quiz aggregate the admin
// list renders alongside each row. LastFinishedAt is nil for a
// player who has never finished a quiz.
type PlayerStats struct {
	PlayerID       int64
	FinishedCount  int64
	LastFinishedAt *time.Time
}

// PlayerLister is the read-only persistence interface the admin
// players page consumes (#423). Kept separate from PlayerStore so the
// admin-facing surface does not bleed into the auth flow; the same
// concrete store satisfies both interfaces (mirrors how PlayerStore
// and OAuthIdentityStore share an instance).
type PlayerLister interface {
	// ListAllPlayers returns one page of players ordered by created_at
	// DESC, plus a stable secondary id ordering for ties.
	ListAllPlayers(ctx context.Context, limit, offset int64) ([]*PlayerListRow, error)
	// CountAllPlayers returns the total number of players for
	// pagination's page-count math.
	CountAllPlayers(ctx context.Context) (int64, error)
	// ListPlayerFinishStats returns the finished-quiz aggregate for the
	// supplied player ids. A player with no finished games is absent
	// from the result; the caller should treat missing entries as
	// (count = 0, last = nil).
	ListPlayerFinishStats(ctx context.Context, playerIDs []int64) ([]*PlayerStats, error)
}

// PlayerStore is the persistence interface used by the auth package.
type PlayerStore interface {
	// GetPlayerByUsername returns the player with the given username.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByUsername(ctx context.Context, username string) (*Player, error)
	// GetPlayerByID returns the player with the given ID.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
	// CreatePlayer creates a player with the given credentials. The
	// store trims + lowercases the email and atomically promotes the
	// first password-bearing registrant to admin. Returns
	// ErrUsernameTaken / ErrEmailTaken on UNIQUE collisions.
	CreatePlayer(ctx context.Context, username, email, passwordHash, requestedRole string) (*Player, error)
	// CreateAnonymousPlayer creates a row with the given username, no email,
	// no password_hash, and role = "player". Used by EnsurePlayer to back a
	// brand-new visitor with a real row before they can play. The caller
	// generates the username (commonly a GeneratePetname result, with an
	// xid-backed "anon-<xid>" form as the last-resort fallback) so the
	// store stays agnostic about the format.
	// Returns ErrUsernameTaken when the username collides on the UNIQUE
	// index; the petname caller treats this as a retry signal.
	CreateAnonymousPlayer(ctx context.Context, username string) (*Player, error)
	// ClaimPlayer upgrades an anonymous row (password_hash IS NULL) in place
	// with the supplied credentials and requested role. The store mirrors the
	// "first password-bearing registrant becomes admin" promotion logic of
	// CreatePlayer so the rule still triggers when a visitor's very first
	// sign-up flows through the claim path.
	// Returns ErrPlayerAlreadyClaimed when the target row already has a
	// password_hash, ErrUsernameTaken when the requested username collides
	// with another row, and ErrPlayerNotFound when the id does not exist.
	ClaimPlayer(
		ctx context.Context,
		playerID int64,
		username, email, passwordHash, requestedRole string,
	) (*Player, error)
	// SetPlayerPasswordHash overwrites the password_hash on the row identified
	// by username. Used by the operator-only -reset-password tool to rotate a
	// forgotten admin password. Returns ErrPlayerNotFound when no row matches.
	SetPlayerPasswordHash(ctx context.Context, username, passwordHash string) error
	// UpdatePlayerUsername renames an anonymous (password_hash IS NULL) row
	// in place. The session cookie keeps pointing at the same row, so the
	// caller stays "logged in" as the same player; the player remains
	// anonymous after the rename.
	// Returns ErrUsernameEmpty when the supplied username trims to the empty
	// string, ErrUsernameTaken when the requested username is already in use,
	// ErrPlayerNotAnonymous when the target row already carries a
	// password_hash (i.e. it is no longer a valid target for a username-only
	// update), and ErrPlayerNotFound when no row matches the id.
	UpdatePlayerUsername(ctx context.Context, playerID int64, username string) (*Player, error)
	// RenamePlayer changes the display name on any player row, regardless
	// of password_hash / email / role. Used by the profile-page rename
	// endpoint so authenticated players (password, OAuth, admin) can
	// update their own name; anonymous rows still go through
	// UpdatePlayerUsername.
	// Returns ErrUsernameEmpty for whitespace-only input, ErrUsernameTaken
	// on a UNIQUE collision, and ErrPlayerNotFound when the id does not
	// exist.
	RenamePlayer(ctx context.Context, playerID int64, username string) (*Player, error)
}
