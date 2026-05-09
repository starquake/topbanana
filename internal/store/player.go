package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/db"
)

// PlayerStore is a wrapper around database operations for managing players.
type PlayerStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewPlayerStore initializes a new PlayerStore with the provided database connection and returns it.
func NewPlayerStore(conn *sql.DB, logger *slog.Logger) *PlayerStore {
	return &PlayerStore{q: db.New(conn), db: conn, logger: logger}
}

// Ping checks the connection to the database.
func (s *PlayerStore) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// GetPlayerByUsername returns the player with the given username.
// Returns auth.ErrPlayerNotFound if no player matches the username.
//
// Whitespace around the username is trimmed before lookup so callers cannot
// accidentally treat "alice" and " alice " as different users. The matching
// trim happens in CreatePlayer too — defense in depth at the storage layer.
func (s *PlayerStore) GetPlayerByUsername(ctx context.Context, username string) (*auth.Player, error) {
	row, err := s.q.GetPlayerByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player by username: %w", err)
	}

	return playerFromRow(row), nil
}

// GetPlayerByID returns the player with the given ID.
// Returns auth.ErrPlayerNotFound if no player matches the ID.
func (s *PlayerStore) GetPlayerByID(ctx context.Context, id int64) (*auth.Player, error) {
	row, err := s.q.GetPlayer(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player by id: %w", err)
	}

	return playerFromRow(row), nil
}

// CreatePlayer creates a new player with the given username, password hash, and
// requested role. The role stored may be promoted to admin by the underlying
// query: if the requested role is "admin" it is honoured directly, and if the
// requested role is anything else it is promoted to admin only when no other
// password-bearing player exists yet (so the first registrant always becomes
// admin atomically — see the SQL for why).
//
// Returns auth.ErrUsernameTaken if the username is already in use.
func (s *PlayerStore) CreatePlayer(
	ctx context.Context,
	username, passwordHash, requestedRole string,
) (*auth.Player, error) {
	row, err := s.q.CreatePlayerWithCredentials(ctx, db.CreatePlayerWithCredentialsParams{
		Username:      strings.TrimSpace(username),
		PasswordHash:  sql.NullString{String: passwordHash, Valid: true},
		RequestedRole: requestedRole,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrUsernameTaken
		}

		return nil, fmt.Errorf("failed to create player: %w", err)
	}

	return playerFromRow(row), nil
}

// CreateAnonymousPlayer inserts a row with the given username, no email, no
// password_hash, role = 'player'. Callers (EnsurePlayer) generate a random
// "anon-<xid>" username so collisions on the unique index are not a concern;
// if one happens anyway it surfaces as auth.ErrUsernameTaken.
func (s *PlayerStore) CreateAnonymousPlayer(ctx context.Context, username string) (*auth.Player, error) {
	row, err := s.q.CreateAnonymousPlayer(ctx, strings.TrimSpace(username))
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrUsernameTaken
		}

		return nil, fmt.Errorf("failed to create anonymous player: %w", err)
	}

	return playerFromRow(row), nil
}

// ClaimPlayer upgrades an anonymous (password_hash IS NULL) row to a
// credentialled player. The underlying SQL uses the same "first
// password-bearing registrant becomes admin" CASE as CreatePlayer so a first
// sign-up that flows through the claim path still triggers the promotion
// atomically.
//
// Returns auth.ErrUsernameTaken when the new username collides with another
// row, auth.ErrPlayerAlreadyClaimed when the row already has credentials
// (the WHERE password_hash IS NULL guard filters it out and the UPDATE
// returns no rows), and auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) ClaimPlayer(
	ctx context.Context,
	playerID int64,
	username, passwordHash, requestedRole string,
) (*auth.Player, error) {
	row, err := s.q.ClaimPlayer(ctx, db.ClaimPlayerParams{
		Username:      strings.TrimSpace(username),
		PasswordHash:  sql.NullString{String: passwordHash, Valid: true},
		RequestedRole: requestedRole,
		ID:            playerID,
	})
	if err != nil {
		return nil, s.classifyClaimErr(ctx, playerID, err)
	}

	return playerFromRow(row), nil
}

// SetPlayerPasswordHash overwrites the password_hash on the row identified
// by username. Returns auth.ErrPlayerNotFound when no row matches; intended
// for the cmd/server -reset-password operator tool, not the public auth flow.
func (s *PlayerStore) SetPlayerPasswordHash(ctx context.Context, username, passwordHash string) error {
	rows, err := s.q.SetPlayerPasswordHash(ctx, db.SetPlayerPasswordHashParams{
		PasswordHash: sql.NullString{String: passwordHash, Valid: true},
		Username:     strings.TrimSpace(username),
	})
	if err != nil {
		return fmt.Errorf("failed to set password hash: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// classifyClaimErr maps a ClaimPlayer storage error onto the auth-package
// sentinels. [sql.ErrNoRows] from the UPDATE is ambiguous (the id might
// not exist OR the row has already been claimed), so it re-queries by id
// to disambiguate.
func (s *PlayerStore) classifyClaimErr(ctx context.Context, playerID int64, err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return auth.ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to claim player: %w", err)
	}

	if _, getErr := s.q.GetPlayer(ctx, playerID); getErr != nil {
		if errors.Is(getErr, sql.ErrNoRows) {
			return auth.ErrPlayerNotFound
		}

		return fmt.Errorf("failed to verify player after claim: %w", getErr)
	}

	return auth.ErrPlayerAlreadyClaimed
}

func playerFromRow(row db.Player) *auth.Player {
	p := &auth.Player{
		ID:        row.ID,
		Username:  row.Username,
		Role:      row.Role,
		CreatedAt: row.CreatedAt,
	}
	if row.Email.Valid {
		p.Email = row.Email.String
	}
	if row.PasswordHash.Valid {
		p.PasswordHash = row.PasswordHash.String
	}

	return p
}
