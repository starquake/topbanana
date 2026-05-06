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

// Player represents an authenticated user (admin or player).
type Player struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// PlayerStore is the persistence interface used by the auth package.
type PlayerStore interface {
	// CountPlayers returns the total number of players. Used to detect the first registration.
	CountPlayers(ctx context.Context) (int64, error)
	// GetPlayerByUsername returns the player with the given username.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByUsername(ctx context.Context, username string) (*Player, error)
	// GetPlayerByID returns the player with the given ID.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
	// CreatePlayer creates a new player with the given username, password hash, and role.
	// Returns ErrUsernameTaken when the username is already in use.
	CreatePlayer(ctx context.Context, username, passwordHash, role string) (*Player, error)
}
