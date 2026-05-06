package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

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

// CountPlayers returns the total number of players in the database.
func (s *PlayerStore) CountPlayers(ctx context.Context) (int64, error) {
	count, err := s.q.CountPlayers(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count players: %w", err)
	}

	return count, nil
}

// GetPlayerByUsername returns the player with the given username.
// Returns auth.ErrPlayerNotFound if no player matches the username.
func (s *PlayerStore) GetPlayerByUsername(ctx context.Context, username string) (*auth.Player, error) {
	row, err := s.q.GetPlayerByUsername(ctx, username)
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

// CreatePlayer creates a new player with the given username, password hash, and role.
// Returns auth.ErrUsernameTaken if the username is already in use.
func (s *PlayerStore) CreatePlayer(
	ctx context.Context,
	username, passwordHash, role string,
) (*auth.Player, error) {
	row, err := s.q.CreatePlayerWithCredentials(ctx, db.CreatePlayerWithCredentialsParams{
		Username:     username,
		PasswordHash: sql.NullString{String: passwordHash, Valid: true},
		Role:         role,
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
