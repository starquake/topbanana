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

// Player represents an authenticated user (admin or player).
type Player struct {
	ID              int64
	Username        string
	Email           string
	PasswordHash    string
	Role            string
	CreatedAt       time.Time
	UsernameClaimed bool
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
// PATCH /api/players/me or via the register flow. The frontend uses
// this to decide whether to show the claim affordances; the existing
// IsAnonymous() check (no password) is a different concern.
func (p *Player) HasCustomName() bool {
	return p.UsernameClaimed
}

// PlayerStore is the persistence interface used by the auth package.
type PlayerStore interface {
	// GetPlayerByUsername returns the player with the given username.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByUsername(ctx context.Context, username string) (*Player, error)
	// GetPlayerByID returns the player with the given ID.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
	// CreatePlayer creates a new player with the given username, password hash,
	// and requested role. The store may promote the stored role to admin when
	// the requested role is not "admin" but there are no other password-bearing
	// players yet — making the "first registrant becomes admin" rule atomic
	// against concurrent registrations.
	// Returns ErrUsernameTaken when the username is already in use.
	CreatePlayer(ctx context.Context, username, passwordHash, requestedRole string) (*Player, error)
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
	ClaimPlayer(ctx context.Context, playerID int64, username, passwordHash, requestedRole string) (*Player, error)
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
}
