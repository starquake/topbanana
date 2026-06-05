package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rs/xid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/livesession"
)

// LiveSessionStore is the SQLite-backed implementation of
// [livesession.Store] for hosted live sessions (MP-1 / #678).
type LiveSessionStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewLiveSessionStore initializes a LiveSessionStore with the provided
// database connection and logger.
func NewLiveSessionStore(conn *sql.DB, logger *slog.Logger) *LiveSessionStore {
	return &LiveSessionStore{q: db.New(conn), db: conn, logger: logger}
}

// Ping verifies the database connection.
func (s *LiveSessionStore) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// CreateSession inserts a sessions row with a fresh xid and the
// caller-supplied join code, populating s.ID / s.CreatedAt / s.Phase from
// the returned row. A join_code UNIQUE collision (the loser of a probe
// race in the service) surfaces as [livesession.ErrJoinCodeUnavailable].
func (s *LiveSessionStore) CreateSession(ctx context.Context, sess *livesession.Session) error {
	row, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:           xid.New().String(),
		QuizID:       sess.QuizID,
		HostPlayerID: sess.HostPlayerID,
		JoinCode:     sess.JoinCode,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return livesession.ErrJoinCodeUnavailable
		}

		return fmt.Errorf("failed to create session: %w", err)
	}

	applySessionRow(sess, row)

	return nil
}

// JoinCodeExists reports whether a session already uses the candidate join
// code.
func (s *LiveSessionStore) JoinCodeExists(ctx context.Context, joinCode string) (bool, error) {
	exists, err := s.q.JoinCodeExists(ctx, joinCode)
	if err != nil {
		return false, fmt.Errorf("failed to check join code exists: %w", err)
	}

	return exists, nil
}

// GetSessionByJoinCode resolves a room code to its session with the lobby
// roster populated. Returns [livesession.ErrSessionNotFound] when no
// session uses the code.
func (s *LiveSessionStore) GetSessionByJoinCode(
	ctx context.Context, joinCode string,
) (*livesession.Session, error) {
	row, err := s.q.GetSessionByJoinCode(ctx, joinCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, livesession.ErrSessionNotFound
		}

		return nil, fmt.Errorf("failed to get session by join code: %w", err)
	}

	sess := sessionFromRow(row)
	sess.Players, err = s.listPlayers(ctx, sess.ID)
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// AddPlayer adds (or revives on re-join) a roster row for the player under
// the requested display name. Returns [livesession.ErrDisplayNameTaken] on
// a per-session display-name collision so the service can fall back to a
// petname.
func (s *LiveSessionStore) AddPlayer(
	ctx context.Context, sessionID string, playerID int64, displayName string,
) (*livesession.Player, error) {
	row, err := s.q.UpsertSessionPlayer(ctx, db.UpsertSessionPlayerParams{
		SessionID:   sessionID,
		PlayerID:    playerID,
		DisplayName: displayName,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, livesession.ErrDisplayNameTaken
		}

		return nil, fmt.Errorf("failed to add session player: %w", err)
	}

	return playerFromSessionRow(row), nil
}

// SetReady toggles a participant's ready flag. Returns
// [livesession.ErrNotParticipant] when the UPDATE matches no roster row
// (the player has not joined the session).
//
//nolint:revive // ready is the desired boolean state of the ready toggle (a value to store), not a behavioural mode switch.
func (s *LiveSessionStore) SetReady(
	ctx context.Context, sessionID string, playerID int64, ready bool,
) error {
	var isReady int64
	if ready {
		isReady = 1
	}
	res, err := s.q.SetSessionPlayerReady(ctx, db.SetSessionPlayerReadyParams{
		IsReady:   isReady,
		SessionID: sessionID,
		PlayerID:  playerID,
	})
	if err != nil {
		return fmt.Errorf("failed to set session player ready: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotParticipant
	}

	return nil
}

// listPlayers loads the lobby roster for a session in join order.
func (s *LiveSessionStore) listPlayers(ctx context.Context, sessionID string) ([]*livesession.Player, error) {
	rows, err := s.q.ListSessionPlayers(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list session players for %q: %w", sessionID, err)
	}

	players := make([]*livesession.Player, 0, len(rows))
	for _, r := range rows {
		players = append(players, playerFromSessionRow(r))
	}

	return players, nil
}

// sessionFromRow maps a generated sessions row onto the domain type
// (without the roster, which the caller fans out separately).
func sessionFromRow(row db.Session) *livesession.Session {
	sess := &livesession.Session{
		ID:           row.ID,
		QuizID:       row.QuizID,
		HostPlayerID: row.HostPlayerID,
		JoinCode:     row.JoinCode,
		Phase:        livesession.Phase(row.Phase),
		CreatedAt:    row.CreatedAt,
	}
	if row.StartedAt.Valid {
		sess.StartedAt = &row.StartedAt.Time
	}
	if row.FinishedAt.Valid {
		sess.FinishedAt = &row.FinishedAt.Time
	}

	return sess
}

// applySessionRow copies the DB-assigned fields back onto a session the
// caller built for an insert.
func applySessionRow(sess *livesession.Session, row db.Session) {
	sess.ID = row.ID
	sess.JoinCode = row.JoinCode
	sess.Phase = livesession.Phase(row.Phase)
	sess.CreatedAt = row.CreatedAt
	if row.StartedAt.Valid {
		sess.StartedAt = &row.StartedAt.Time
	}
	if row.FinishedAt.Valid {
		sess.FinishedAt = &row.FinishedAt.Time
	}
}

// playerFromSessionRow maps a generated session_players row onto the
// domain roster type.
func playerFromSessionRow(row db.SessionPlayer) *livesession.Player {
	return &livesession.Player{
		ID:          row.ID,
		SessionID:   row.SessionID,
		PlayerID:    row.PlayerID,
		DisplayName: row.DisplayName,
		IsReady:     row.IsReady != 0,
		JoinedAt:    row.JoinedAt,
		LastSeenAt:  row.LastSeenAt,
	}
}
