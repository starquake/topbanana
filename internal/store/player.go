package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

// UpdatePlayerUsername renames an anonymous (password_hash IS NULL) row in
// place so an anonymous visitor can pick their own display name without
// going through the full claim flow. The session cookie continues to point
// at the same row, so the player stays "signed in" as the same player and
// remains anonymous after the rename.
//
// The username is trimmed before storage to mirror CreatePlayer's
// normalisation; lookups in GetPlayerByUsername perform the same trim so
// "alice" and " alice " cannot become distinct identities.
//
// Returns auth.ErrUsernameTaken when the requested username collides with
// another row, and auth.ErrPlayerNotAnonymous when the target row exists
// but already has a password_hash (the WHERE guard filters it out and the
// UPDATE returns no rows). An unknown player ID also yields no rows; the
// wrapper re-queries by id to disambiguate, returning ErrPlayerNotFound
// when the row genuinely does not exist.
func (s *PlayerStore) UpdatePlayerUsername(
	ctx context.Context,
	playerID int64,
	username string,
) (*auth.Player, error) {
	cleaned := strings.TrimSpace(username)
	if cleaned == "" {
		return nil, auth.ErrUsernameEmpty
	}

	row, err := s.q.UpdatePlayerUsername(ctx, db.UpdatePlayerUsernameParams{
		Username: cleaned,
		ID:       playerID,
	})
	if err != nil {
		return nil, s.classifyUpdateUsernameErr(ctx, playerID, err)
	}

	return playerFromRow(row), nil
}

// RenamePlayer changes the display name on an arbitrary player row,
// not just an anonymous one. Used by the profile-page rename endpoint
// (POST /profile/username, #410) so authenticated players (password,
// OAuth, admin) can edit their own name. Returns auth.ErrUsernameEmpty
// when the input trims to "", auth.ErrUsernameTaken on a UNIQUE
// collision, and auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) RenamePlayer(
	ctx context.Context,
	playerID int64,
	username string,
) (*auth.Player, error) {
	cleaned := strings.TrimSpace(username)
	if cleaned == "" {
		return nil, auth.ErrUsernameEmpty
	}

	row, err := s.q.RenamePlayer(ctx, db.RenamePlayerParams{
		Username: cleaned,
		ID:       playerID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrUsernameTaken
		}

		return nil, fmt.Errorf("failed to rename player: %w", err)
	}

	return playerFromRow(row), nil
}

// GetPlayerByEmail returns the player whose email matches. Returns
// auth.ErrPlayerNotFound when no row matches. The email is wrapped in a
// [sql.NullString] with Valid=true so a literal NULL row never matches a
// caller-supplied empty string.
func (s *PlayerStore) GetPlayerByEmail(ctx context.Context, email string) (*auth.Player, error) {
	row, err := s.q.GetPlayerByEmail(ctx, sql.NullString{String: email, Valid: true})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player by email: %w", err)
	}

	return playerFromRow(row), nil
}

// GetPlayerByProviderSubject returns the player whose player_identities
// row matches (provider, subject). Returns auth.ErrPlayerNotFound when
// no identity is linked yet.
func (s *PlayerStore) GetPlayerByProviderSubject(
	ctx context.Context,
	provider, subject string,
) (*auth.Player, error) {
	row, err := s.q.GetPlayerByProviderSubject(ctx, db.GetPlayerByProviderSubjectParams{
		Provider: provider,
		Subject:  subject,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player by provider subject: %w", err)
	}

	return playerFromRow(row), nil
}

// CreatePlayerFromOAuth inserts a new players row with the supplied
// username + email and no password_hash. Returns auth.ErrUsernameTaken
// when the username collides (the OAuth handler retries on this
// sentinel with a fresh petname).
func (s *PlayerStore) CreatePlayerFromOAuth(
	ctx context.Context,
	username, email string,
) (*auth.Player, error) {
	row, err := s.q.CreatePlayerFromOAuth(ctx, db.CreatePlayerFromOAuthParams{
		Username: strings.TrimSpace(username),
		Email:    sql.NullString{String: email, Valid: true},
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrUsernameTaken
		}

		return nil, fmt.Errorf("failed to create player from oauth: %w", err)
	}

	return playerFromRow(row), nil
}

// ClaimPlayerForOAuth attaches an OAuth-verified email to an existing
// anonymous (no password_hash, no email) players row. Returns
// auth.ErrPlayerNotFound when the row does not match the
// anonymous-only guards in the SQL — the OAuth handler treats that
// sentinel as "fall through to create a new row" so a session
// pointing at a deleted, credentialled, or already-OAuth-linked row
// degrades gracefully.
func (s *PlayerStore) ClaimPlayerForOAuth(
	ctx context.Context,
	playerID int64,
	email string,
) (*auth.Player, error) {
	row, err := s.q.ClaimPlayerForOAuth(ctx, db.ClaimPlayerForOAuthParams{
		ID:    playerID,
		Email: sql.NullString{String: email, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to claim player for oauth: %w", err)
	}

	return playerFromRow(row), nil
}

// LinkProviderIdentity inserts a player_identities row tying the given
// player to the (provider, subject) pair. Returns
// auth.ErrIdentityAlreadyLinked when the UNIQUE (provider, subject)
// constraint fires; the caller treats this as "another request beat us
// to it" and re-reads the identity row.
func (s *PlayerStore) LinkProviderIdentity(ctx context.Context, playerID int64, provider, subject string) error {
	err := s.q.LinkProviderIdentity(ctx, db.LinkProviderIdentityParams{
		PlayerID: playerID,
		Provider: provider,
		Subject:  subject,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return auth.ErrIdentityAlreadyLinked
		}

		return fmt.Errorf("failed to link provider identity: %w", err)
	}

	return nil
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

// ListAllPlayers returns a page of players for the admin players
// list (#423). The page size and offset come straight from the
// handler's pagination params; the SQL handles the bounds + ordering.
func (s *PlayerStore) ListAllPlayers(ctx context.Context, limit, offset int64) ([]*auth.PlayerListRow, error) {
	rows, err := s.q.ListAllPlayers(ctx, db.ListAllPlayersParams{
		RowLimit:  limit,
		RowOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list all players: %w", err)
	}

	out := make([]*auth.PlayerListRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, &auth.PlayerListRow{
			ID:            r.ID,
			Username:      r.Username,
			Email:         r.Email.String,
			Role:          r.Role,
			HasPassword:   r.PasswordHash.Valid,
			HasOAuth:      r.HasOauth,
			OAuthProvider: r.OauthProvider,
			CreatedAt:     r.CreatedAt,
		})
	}

	return out, nil
}

// CountAllPlayers returns the total number of players for the admin
// list's pagination math.
func (s *PlayerStore) CountAllPlayers(ctx context.Context) (int64, error) {
	count, err := s.q.CountAllPlayers(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count players: %w", err)
	}

	return count, nil
}

// ListPlayerFinishStats returns the finished-quiz aggregate for the
// supplied player IDs. Players with zero finished games are absent
// from the result; the caller treats a missing entry as (count = 0,
// last = nil). The query short-circuits on an empty id slice so the
// admin list does not issue a `WHERE id IN ()` against the DB.
func (s *PlayerStore) ListPlayerFinishStats(ctx context.Context, playerIDs []int64) ([]*auth.PlayerStats, error) {
	if len(playerIDs) == 0 {
		return nil, nil
	}

	rows, err := s.q.ListPlayerFinishStats(ctx, playerIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list player finish stats: %w", err)
	}

	out := make([]*auth.PlayerStats, 0, len(rows))
	for _, r := range rows {
		out = append(out, &auth.PlayerStats{
			PlayerID:       r.PlayerID,
			FinishedCount:  r.FinishedCount,
			LastFinishedAt: parseSQLiteTimestamp(r.LastFinishedAt),
		})
	}

	return out, nil
}

// parseSQLiteTimestamp accepts the two formats modernc.org/sqlite can
// return for an aggregate over a DATETIME column ("YYYY-MM-DD
// HH:MM:SS" if the column was written via SQLite helpers, RFC3339 if
// written via [time.Time]). An unparseable value falls through to nil
// so a single malformed row does not fail the whole admin list page.
func parseSQLiteTimestamp(raw string) *time.Time {
	const sqliteDateTime = "2006-01-02 15:04:05"
	if t, err := time.Parse(sqliteDateTime, raw); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}

	return nil
}

// classifyUpdateUsernameErr maps an UpdatePlayerUsername storage error onto
// the auth-package sentinels. [sql.ErrNoRows] from the UPDATE is ambiguous
// (the id might not exist OR the row already carries a password_hash), so it
// re-queries by id to disambiguate.
func (s *PlayerStore) classifyUpdateUsernameErr(ctx context.Context, playerID int64, err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return auth.ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to update player username: %w", err)
	}

	if _, getErr := s.q.GetPlayer(ctx, playerID); getErr != nil {
		if errors.Is(getErr, sql.ErrNoRows) {
			return auth.ErrPlayerNotFound
		}

		return fmt.Errorf("failed to verify player after username update: %w", getErr)
	}

	return auth.ErrPlayerNotAnonymous
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
		ID:              row.ID,
		Username:        row.Username,
		Role:            row.Role,
		CreatedAt:       row.CreatedAt,
		UsernameClaimed: row.UsernameClaimed != 0,
	}
	if row.Email.Valid {
		p.Email = row.Email.String
	}
	if row.PasswordHash.Valid {
		p.PasswordHash = row.PasswordHash.String
	}

	return p
}
