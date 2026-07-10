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
	"github.com/starquake/topbanana/internal/database"
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

// GetPlayerByDisplayName returns the player with the given display name.
// Returns auth.ErrPlayerNotFound if no player matches the display name.
//
// Whitespace around the display name is trimmed before lookup so callers cannot
// accidentally treat "alice" and " alice " as different users. The matching
// trim happens in CreatePlayer too - defense in depth at the storage layer.
func (s *PlayerStore) GetPlayerByDisplayName(ctx context.Context, displayName string) (*auth.Player, error) {
	row, err := s.q.GetPlayerByDisplayName(ctx, strings.TrimSpace(displayName))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player by display name: %w", err)
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

// CreatePlayer creates a credentialled player. Email is trimmed and
// lowercased before insert. Returns auth.ErrDisplayNameTaken or
// auth.ErrEmailTaken on UNIQUE collisions; the underlying SQL
// promotes the first password-bearing registrant to admin.
func (s *PlayerStore) CreatePlayer(
	ctx context.Context,
	displayName, email, passwordHash, requestedRole string,
) (*auth.Player, error) {
	cleanedDisplayName := strings.TrimSpace(displayName)
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	row, err := s.q.CreatePlayerWithCredentials(ctx, db.CreatePlayerWithCredentialsParams{
		DisplayName:   cleanedDisplayName,
		PasswordHash:  sql.NullString{String: passwordHash, Valid: true},
		Email:         sql.NullString{String: cleanedEmail, Valid: cleanedEmail != ""},
		RequestedRole: requestedRole,
	})
	if err != nil {
		return nil, s.classifyCredentialConflict(ctx, cleanedDisplayName, cleanedEmail, err)
	}

	return playerFromRow(row), nil
}

// CreateAnonymousPlayer inserts a row with the given displayName, no email, no
// password_hash, role = 'player'. Callers (EnsurePlayer) generate a random
// "anon-<xid>" displayName so collisions on the unique index are not a concern;
// if one happens anyway it surfaces as auth.ErrDisplayNameTaken.
func (s *PlayerStore) CreateAnonymousPlayer(ctx context.Context, displayName string) (*auth.Player, error) {
	row, err := s.q.CreateAnonymousPlayer(ctx, strings.TrimSpace(displayName))
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrDisplayNameTaken
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
// Returns auth.ErrDisplayNameTaken when the new displayName collides with another
// row, auth.ErrPlayerAlreadyClaimed when the row already has credentials
// (the WHERE password_hash IS NULL guard filters it out and the UPDATE
// returns no rows), and auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) ClaimPlayer(
	ctx context.Context,
	playerID int64,
	displayName, email, passwordHash, requestedRole string,
) (*auth.Player, error) {
	cleanedDisplayName := strings.TrimSpace(displayName)
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	row, err := s.q.ClaimPlayer(ctx, db.ClaimPlayerParams{
		DisplayName:   cleanedDisplayName,
		PasswordHash:  sql.NullString{String: passwordHash, Valid: true},
		Email:         sql.NullString{String: cleanedEmail, Valid: cleanedEmail != ""},
		RequestedRole: requestedRole,
		ID:            playerID,
	})
	if err != nil {
		return nil, s.classifyClaimErr(ctx, playerID, cleanedDisplayName, cleanedEmail, err)
	}

	return playerFromRow(row), nil
}

// UpdatePlayerDisplayName renames an anonymous (password_hash IS NULL)
// row in place. Returns ErrDisplayNameTaken on collision,
// ErrPlayerNotAnonymous when the row already has a password_hash
// (filtered out by the WHERE guard), and ErrPlayerNotFound when the
// row genuinely does not exist (disambiguated by a follow-up query).
func (s *PlayerStore) UpdatePlayerDisplayName(
	ctx context.Context,
	playerID int64,
	displayName string,
) (*auth.Player, error) {
	cleaned := strings.TrimSpace(displayName)
	if cleaned == "" {
		return nil, auth.ErrDisplayNameEmpty
	}

	row, err := s.q.UpdatePlayerDisplayName(ctx, db.UpdatePlayerDisplayNameParams{
		DisplayName: cleaned,
		ID:          playerID,
	})
	if err != nil {
		return nil, s.classifyUpdateDisplayNameErr(ctx, playerID, err)
	}

	return playerFromRow(row), nil
}

// RenamePlayer changes the display name on an arbitrary player row,
// not just an anonymous one. Used by the profile-page rename endpoint
// (POST /profile/display-name, #410) so authenticated players (password,
// OAuth, admin) can edit their own name. Returns auth.ErrDisplayNameEmpty
// when the input trims to "", auth.ErrDisplayNameTaken on a UNIQUE
// collision, and auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) RenamePlayer(
	ctx context.Context,
	playerID int64,
	displayName string,
) (*auth.Player, error) {
	cleaned := strings.TrimSpace(displayName)
	if cleaned == "" {
		return nil, auth.ErrDisplayNameEmpty
	}

	row, err := s.q.RenamePlayer(ctx, db.RenamePlayerParams{
		DisplayName: cleaned,
		ID:          playerID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrDisplayNameTaken
		}

		return nil, fmt.Errorf("failed to rename player: %w", err)
	}

	return playerFromRow(row), nil
}

// AdminRenamePlayer changes the display name on any row, leaving
// display_name_claimed untouched so an admin tidying a guest's auto-petname does
// not promote the row to a self-claimed account. Returns auth.ErrDisplayNameEmpty
// for whitespace-only input, auth.ErrDisplayNameTaken on a UNIQUE collision, and
// auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) AdminRenamePlayer(
	ctx context.Context,
	playerID int64,
	displayName string,
) (*auth.Player, error) {
	cleaned := strings.TrimSpace(displayName)
	if cleaned == "" {
		return nil, auth.ErrDisplayNameEmpty
	}

	row, err := s.q.AdminRenamePlayer(ctx, db.AdminRenamePlayerParams{
		DisplayName: cleaned,
		ID:          playerID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrDisplayNameTaken
		}

		return nil, fmt.Errorf("failed to admin-rename player: %w", err)
	}

	return playerFromRow(row), nil
}

// GetPlayerByEmail returns the player whose email matches. Returns
// auth.ErrPlayerNotFound when no row matches. The email is wrapped in a
// [sql.NullString] with Valid=true so a literal NULL row never matches a
// caller-supplied empty string. The argument is lowercased + trimmed to
// match how CreatePlayer / ClaimPlayer / CreatePlayerFromOAuth store it,
// so a mixed-case OIDC email finds the existing row instead of creating
// a duplicate (#471).
func (s *PlayerStore) GetPlayerByEmail(ctx context.Context, email string) (*auth.Player, error) {
	cleaned := strings.ToLower(strings.TrimSpace(email))
	row, err := s.q.GetPlayerByEmail(ctx, sql.NullString{String: cleaned, Valid: true})
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
// displayName + email and no password_hash. Returns auth.ErrDisplayNameTaken
// when the displayName collides (the OAuth handler retries on this
// sentinel with a fresh petname).
func (s *PlayerStore) CreatePlayerFromOAuth(
	ctx context.Context,
	displayName, email string,
) (*auth.Player, error) {
	row, err := s.q.CreatePlayerFromOAuth(ctx, db.CreatePlayerFromOAuthParams{
		DisplayName: strings.TrimSpace(displayName),
		Email:       sql.NullString{String: strings.ToLower(strings.TrimSpace(email)), Valid: true},
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return nil, auth.ErrDisplayNameTaken
		}

		return nil, fmt.Errorf("failed to create player from oauth: %w", err)
	}

	return playerFromRow(row), nil
}

// ClaimPlayerForOAuth attaches an OAuth-verified email to an existing
// anonymous (no password_hash, no email) players row. Returns
// auth.ErrPlayerNotFound when the row does not match the
// anonymous-only guards in the SQL - the OAuth handler treats that
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
		Email: sql.NullString{String: strings.ToLower(strings.TrimSpace(email)), Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to claim player for oauth: %w", err)
	}

	return playerFromRow(row), nil
}

// MarkPlayerEmailVerifiedIfNew stamps email_verified_at when it is
// currently NULL. Idempotent.
func (s *PlayerStore) MarkPlayerEmailVerifiedIfNew(ctx context.Context, playerID int64) error {
	if _, err := s.q.MarkPlayerEmailVerifiedIfNew(ctx, playerID); err != nil {
		return fmt.Errorf("failed to mark player email verified: %w", err)
	}

	return nil
}

// CreateVerifyToken inserts a row in email_verify_tokens with the given
// hash, player id, and absolute expiry. expiresAt is normalised to UTC
// so the driver's [time.Time.String] text encoding lines up
// lexicographically with the UTC clock the consume/sweep paths read -
// mixing offsets between insert and read silently breaks the string
// comparison.
//
// pendingEmail carries the new address an in-session email-change
// request (#497) wants to switch to; "" mints a register/resend row
// whose consume side just stamps email_verified_at. The column is
// nullable in the schema so an empty string maps to NULL via
// [sql.NullString] with Valid=false.
func (s *PlayerStore) CreateVerifyToken(
	ctx context.Context, tokenHash string, playerID int64, expiresAt time.Time, pendingEmail string,
) error {
	pending := sql.NullString{}
	if pendingEmail != "" {
		pending = sql.NullString{String: pendingEmail, Valid: true}
	}
	if err := s.q.CreateEmailVerifyToken(ctx, db.CreateEmailVerifyTokenParams{
		TokenHash:    tokenHash,
		PlayerID:     playerID,
		ExpiresAt:    expiresAt.UTC(),
		PendingEmail: pending,
	}); err != nil {
		return fmt.Errorf("failed to create verify token: %w", err)
	}

	return nil
}

// ConsumeVerifyToken atomically marks the token row consumed and
// applies the verified-email side effect in the same transaction. The
// side effect depends on the token's pending_email column: NULL rows
// (register / resend variant) stamp email_verified_at when currently
// NULL; non-NULL rows (in-session email-change variant, #497) swap
// players.email to pending_email, re-stamp email_verified_at, and
// bump session_version so a stolen link cannot ride an existing
// cookie. Returns the player id on success,
// auth.ErrVerifyTokenAlreadyUsed if the row exists but was already
// consumed (duplicate click on a stale link), auth.ErrEmailTaken when
// the email-change branch hits the UNIQUE players.email constraint,
// and auth.ErrVerifyTokenInvalid when no row matches. The token row
// is consumed regardless of which branch runs; expired-but-consumed
// cleanup happens via the sweep query at startup.
func (s *PlayerStore) ConsumeVerifyToken(ctx context.Context, tokenHash string) (int64, error) {
	var playerID int64
	now := time.Now().UTC()
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		row, err := q.ConsumeEmailVerifyToken(ctx, db.ConsumeEmailVerifyTokenParams{
			TokenHash:  tokenHash,
			Now:        now,
			ConsumedAt: sql.NullTime{Time: now, Valid: true},
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				ownerID, classifyErr := classifyVerifyTokenMiss(ctx, q, tokenHash)
				playerID = ownerID

				return classifyErr
			}

			return fmt.Errorf("query: %w", err)
		}
		playerID = row.PlayerID

		return applyVerifyTokenSideEffect(ctx, q, row)
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrVerifyTokenAlreadyUsed):
			return playerID, auth.ErrVerifyTokenAlreadyUsed
		case errors.Is(err, auth.ErrVerifyTokenInvalid):
			return 0, auth.ErrVerifyTokenInvalid
		case errors.Is(err, auth.ErrEmailTaken):
			return playerID, auth.ErrEmailTaken
		case errors.Is(err, auth.ErrPlayerNotFound):
			return playerID, auth.ErrPlayerNotFound
		default:
			return 0, fmt.Errorf("consume verify token: %w", err)
		}
	}

	return playerID, nil
}

// applyVerifyTokenSideEffect runs the per-variant follow-up after a
// successful consume. Register/resend rows stamp email_verified_at
// when still NULL; email-change rows (pending_email non-empty) swap
// players.email, re-stamp email_verified_at, and bump session_version
// in a single UPDATE. Split out of ConsumeVerifyToken so the
// transaction body stays under revive's function-length cap.
func applyVerifyTokenSideEffect(
	ctx context.Context, q *db.Queries, row db.ConsumeEmailVerifyTokenRow,
) error {
	if !row.PendingEmail.Valid || row.PendingEmail.String == "" {
		if _, err := q.MarkPlayerEmailVerifiedIfNew(ctx, row.PlayerID); err != nil {
			return fmt.Errorf("failed to stamp email_verified_at: %w", err)
		}

		return nil
	}
	pending := sql.NullString{String: row.PendingEmail.String, Valid: true}
	rows, err := q.SwapPlayerEmail(ctx, db.SwapPlayerEmailParams{
		Email: pending,
		ID:    row.PlayerID,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return auth.ErrEmailTaken
		}

		return fmt.Errorf("failed to swap email: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// classifyVerifyTokenMiss disambiguates the UPDATE-no-rows case. The
// row may exist with consumed_at set (legitimate duplicate click) or
// not exist at all / be expired (invalid). A second SELECT inside the
// same transaction is cheap and avoids leaking "expired vs consumed
// vs never-existed" via response codes. Returns the row's player_id on
// the already-used branch so the handler can detect a session that
// belongs to a different player than the one the token verifies; an
// invalid / missing row returns 0.
func classifyVerifyTokenMiss(ctx context.Context, q *db.Queries, tokenHash string) (int64, error) {
	row, err := q.GetEmailVerifyToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, auth.ErrVerifyTokenInvalid
		}

		return 0, fmt.Errorf("failed to look up verify token: %w", err)
	}
	if row.ConsumedAt.Valid {
		return row.PlayerID, auth.ErrVerifyTokenAlreadyUsed
	}

	return 0, auth.ErrVerifyTokenInvalid
}

// DeleteExpiredVerifyTokens drops expired rows from email_verify_tokens.
// UTC across all sites that read or write expires_at so the driver's
// [time.Time.String] text encoding stays lexicographically comparable
// regardless of the host time zone.
func (s *PlayerStore) DeleteExpiredVerifyTokens(ctx context.Context) error {
	if err := s.q.DeleteExpiredEmailVerifyTokens(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to delete expired verify tokens: %w", err)
	}

	return nil
}

// CreateResetToken inserts a row in password_reset_tokens with the
// given hash, player id, and absolute expiry. expiresAt is normalised
// to UTC so the driver's [time.Time.String] text encoding lines up
// lexicographically with the UTC clock the consume/sweep paths read -
// mixing offsets between insert and read silently breaks the string
// comparison.
func (s *PlayerStore) CreateResetToken(
	ctx context.Context, tokenHash string, playerID int64, expiresAt time.Time,
) error {
	if err := s.q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: tokenHash,
		PlayerID:  playerID,
		ExpiresAt: expiresAt.UTC(),
	}); err != nil {
		return fmt.Errorf("failed to create reset token: %w", err)
	}

	return nil
}

// LookupResetToken returns the owning player id and a liveness flag
// for the supplied token hash. live = true iff the row exists, is
// unconsumed, and is unexpired. Used by the GET /reset-password
// handler to short-circuit form render for dead tokens so the user
// is not asked to type a password the POST will reject. A missing row
// is not an error: the handler treats (0, false, nil) the same as an
// expired or consumed row.
func (s *PlayerStore) LookupResetToken(ctx context.Context, tokenHash string) (int64, bool, error) {
	row, err := s.q.GetPasswordResetToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}

		return 0, false, fmt.Errorf("failed to look up reset token: %w", err)
	}
	live := !row.ConsumedAt.Valid && row.ExpiresAt.After(time.Now().UTC())

	return row.PlayerID, live, nil
}

// ConsumeResetToken atomically marks the reset row consumed, rotates
// password_hash, and bumps session_version - all in one transaction
// so a crash mid-flow cannot leave a player with a consumed token but
// an old password, nor a new password with old sessions still live.
// Returns the player id on success, auth.ErrResetTokenInvalid when no
// live row matches (never existed, expired, or already consumed).
func (s *PlayerStore) ConsumeResetToken(
	ctx context.Context, tokenHash, newPasswordHash string,
) (int64, error) {
	var playerID int64
	now := time.Now().UTC()
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		id, err := q.ConsumePasswordResetToken(ctx, db.ConsumePasswordResetTokenParams{
			TokenHash:  tokenHash,
			Now:        now,
			ConsumedAt: sql.NullTime{Time: now, Valid: true},
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return auth.ErrResetTokenInvalid
			}

			return fmt.Errorf("query: %w", err)
		}
		rows, err := q.ResetPlayerPassword(ctx, db.ResetPlayerPasswordParams{
			ID:           id,
			PasswordHash: sql.NullString{String: newPasswordHash, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("rotate password: %w", err)
		}
		if rows == 0 {
			return auth.ErrResetTokenInvalid
		}
		playerID = id

		return nil
	})
	if err != nil {
		if errors.Is(err, auth.ErrResetTokenInvalid) {
			return 0, auth.ErrResetTokenInvalid
		}

		return 0, fmt.Errorf("consume reset token: %w", err)
	}

	return playerID, nil
}

// DeleteExpiredResetTokens drops expired rows from password_reset_tokens.
// UTC mirrors the email-verify sweep so the lexicographic comparison
// stays consistent across the host timezone.
func (s *PlayerStore) DeleteExpiredResetTokens(ctx context.Context) error {
	if err := s.q.DeleteExpiredPasswordResetTokens(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to delete expired reset tokens: %w", err)
	}

	return nil
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

// ChangePlayerPassword atomically rotates password_hash and bumps
// session_version on the row identified by id. Shares the
// ResetPlayerPassword query with the forgot-password flow: both paths
// want the same "new hash + invalidate other cookies" semantics, only
// the auth proof differs (token vs current password verified by the
// caller). Returns auth.ErrPlayerNotFound when no row matches the id.
func (s *PlayerStore) ChangePlayerPassword(ctx context.Context, playerID int64, passwordHash string) error {
	rows, err := s.q.ResetPlayerPassword(ctx, db.ResetPlayerPasswordParams{
		ID:           playerID,
		PasswordHash: sql.NullString{String: passwordHash, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to change player password: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// SetPlayerPasswordHash overwrites the password_hash on the row identified
// by email. Returns auth.ErrPlayerNotFound when no row matches; intended
// for the cmd/server -reset-password operator tool, not the public auth flow.
// The lookup matches how the post-#446 login flow finds the row, so the
// reset target equals what the player types into /login.
func (s *PlayerStore) SetPlayerPasswordHash(ctx context.Context, email, passwordHash string) error {
	cleaned := strings.ToLower(strings.TrimSpace(email))
	rows, err := s.q.SetPlayerPasswordHash(ctx, db.SetPlayerPasswordHashParams{
		PasswordHash: sql.NullString{String: passwordHash, Valid: true},
		Email:        sql.NullString{String: cleaned, Valid: cleaned != ""},
	})
	if err != nil {
		return fmt.Errorf("failed to set password hash: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// ListPlayersByOnboardingState returns a page of players for the admin
// list (#450), filtered to the supplied onboarding-state bucket. Pass
// [auth.OnboardingStateAll] to disable the filter.
func (s *PlayerStore) ListPlayersByOnboardingState(
	ctx context.Context, state string, limit, offset int64,
) ([]*auth.PlayerListRow, error) {
	rows, err := s.q.ListPlayersByOnboardingState(ctx, db.ListPlayersByOnboardingStateParams{
		State:     state,
		RowLimit:  limit,
		RowOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list players by onboarding state: %w", err)
	}

	out := make([]*auth.PlayerListRow, 0, len(rows))
	for _, r := range rows {
		row := &auth.PlayerListRow{
			ID:              r.ID,
			DisplayName:     r.DisplayName,
			Email:           r.Email.String,
			Role:            r.Role,
			HasPassword:     r.PasswordHash.Valid,
			HasOAuth:        r.HasOauth,
			OAuthProvider:   r.OauthProvider,
			CreatedAt:       r.CreatedAt,
			OnboardingState: r.OnboardingState,
		}
		if r.EmailVerifiedAt.Valid {
			verified := r.EmailVerifiedAt.Time
			row.EmailVerifiedAt = &verified
		}
		if r.ApprovedAt.Valid {
			approved := r.ApprovedAt.Time
			row.ApprovedAt = &approved
		}
		out = append(out, row)
	}

	return out, nil
}

// CountPlayersInOnboardingState returns the number of rows matching
// the supplied state bucket. Pass [auth.OnboardingStateAll] to count
// every row regardless of onboarding state.
func (s *PlayerStore) CountPlayersInOnboardingState(ctx context.Context, state string) (int64, error) {
	count, err := s.q.CountPlayersInOnboardingState(ctx, state)
	if err != nil {
		return 0, fmt.Errorf("failed to count players in onboarding state: %w", err)
	}

	return count, nil
}

// CountPlayersByOnboardingState returns a (state -> count) map across
// every bucket in a single round-trip. Backs the tab-strip counts on
// the admin players list (#450). Buckets with zero rows are absent
// from the map; the caller treats a missing key as zero.
func (s *PlayerStore) CountPlayersByOnboardingState(ctx context.Context) (map[string]int64, error) {
	rows, err := s.q.CountPlayersByOnboardingState(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count players by onboarding state: %w", err)
	}

	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.State] = r.PlayerCount
	}

	return out, nil
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

// GetPlayerDetail returns the per-player detail-view payload for the
// admin page (#450). Returns auth.ErrPlayerNotFound when the id does
// not match a row.
func (s *PlayerStore) GetPlayerDetail(ctx context.Context, id int64) (*auth.PlayerDetail, error) {
	row, err := s.q.GetPlayerWithOnboardingState(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrPlayerNotFound
		}

		return nil, fmt.Errorf("failed to get player detail: %w", err)
	}

	detail := &auth.PlayerDetail{
		ID:              row.ID,
		DisplayName:     row.DisplayName,
		Email:           row.Email.String,
		Role:            row.Role,
		HasPassword:     row.PasswordHash.Valid,
		HasOAuth:        row.HasOauth,
		OAuthProvider:   row.OauthProvider,
		CreatedAt:       row.CreatedAt,
		OnboardingState: row.OnboardingState,
	}
	if row.EmailVerifiedAt.Valid {
		verified := row.EmailVerifiedAt.Time
		detail.EmailVerifiedAt = &verified
	}
	if row.ApprovedAt.Valid {
		approved := row.ApprovedAt.Time
		detail.ApprovedAt = &approved
	}

	return detail, nil
}

// ListRecentFinishedGamesForPlayer returns at most limit finished-game
// rows for the supplied player, newest-first. Empty slice when the
// player has never finished a quiz; the admin per-player detail view
// (#450) renders an empty-state row in that case.
func (s *PlayerStore) ListRecentFinishedGamesForPlayer(
	ctx context.Context, playerID, limit int64,
) ([]*auth.RecentFinishedGame, error) {
	rows, err := s.q.ListRecentFinishedGamesForPlayer(ctx, db.ListRecentFinishedGamesForPlayerParams{
		PlayerID: playerID,
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list recent finished games for player: %w", err)
	}

	out := make([]*auth.RecentFinishedGame, 0, len(rows))
	for _, r := range rows {
		out = append(out, &auth.RecentFinishedGame{
			GameID:    r.GameID,
			QuizID:    r.QuizID,
			QuizTitle: r.QuizTitle,
			CreatedAt: r.CreatedAt,
		})
	}

	return out, nil
}

// ListFinishedSessionPlaysForPlayer returns at most limit finished
// live-quiz plays for the player, newest-first, one row per quiz. Empty
// slice when the player has never finished a live session.
func (s *PlayerStore) ListFinishedSessionPlaysForPlayer(
	ctx context.Context, playerID, limit int64,
) ([]*auth.FinishedSessionPlay, error) {
	rows, err := s.q.ListFinishedSessionPlaysForPlayer(ctx, db.ListFinishedSessionPlaysForPlayerParams{
		PlayerID: playerID,
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list finished session plays for player: %w", err)
	}

	out := make([]*auth.FinishedSessionPlay, 0, len(rows))
	for _, r := range rows {
		// A finished session always has finished_at stamped (SetSessionFinished
		// sets it), so the zero value only ever stands in for an impossible
		// NULL rather than masking real data.
		out = append(out, &auth.FinishedSessionPlay{
			QuizID:     r.QuizID,
			QuizTitle:  r.QuizTitle,
			FinishedAt: r.FinishedAt.Time,
		})
	}

	return out, nil
}

// SetPlayerEmailVerifiedNow stamps email_verified_at to CURRENT_TIMESTAMP
// even when the row is already verified. Used by the admin "Mark
// verified" action (#450). Returns auth.ErrPlayerNotFound when the id
// matches no row.
func (s *PlayerStore) SetPlayerEmailVerifiedNow(ctx context.Context, playerID int64) error {
	rows, err := s.q.SetPlayerEmailVerifiedNow(ctx, playerID)
	if err != nil {
		return fmt.Errorf("failed to set email verified now: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// SetPlayerApprovedNow stamps approved_at when it is currently NULL (#1227),
// reporting whether a row was actually stamped. A false with a nil error means
// the row was already approved (a concurrent approve won), which the caller uses
// to skip a duplicate audit + email; a missing id also reports false (the caller
// pre-checks existence via GetPlayerDetail).
func (s *PlayerStore) SetPlayerApprovedNow(ctx context.Context, playerID int64) (bool, error) {
	rows, err := s.q.SetPlayerApprovedNow(ctx, playerID)
	if err != nil {
		return false, fmt.Errorf("failed to set approved now: %w", err)
	}

	return rows > 0, nil
}

// ListAdminEmails returns the email of every admin with an address on file,
// alphabetically. Backs the awaiting-approval fan-out to admins (#1227).
func (s *PlayerStore) ListAdminEmails(ctx context.Context) ([]string, error) {
	emails, err := s.q.ListAdminEmails(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list admin emails: %w", err)
	}

	return emails, nil
}

// SetPlayerEmail rewrites players.email on the row identified by id and
// clears email_verified_at so the changed address must be re-proven. Used
// by the admin "Set / overwrite email" action (#450); the admin then marks
// the account verified or triggers a resend if the new address should be
// treated as proven. Returns auth.ErrEmailTaken on a UNIQUE collision and
// auth.ErrPlayerNotFound when no row matches.
func (s *PlayerStore) SetPlayerEmail(ctx context.Context, playerID int64, email string) error {
	cleaned := strings.ToLower(strings.TrimSpace(email))
	rows, err := s.q.SetPlayerEmail(ctx, db.SetPlayerEmailParams{
		Email: sql.NullString{String: cleaned, Valid: cleaned != ""},
		ID:    playerID,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return auth.ErrEmailTaken
		}

		return fmt.Errorf("failed to set player email: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// CreatePlayerByAdmin inserts a fresh credentialled row with
// email_verified_at stamped (#450). Email is trimmed + lowercased;
// passwordHash must be non-empty (the handler enforces it before
// reaching the store). role sets the new row's tier. Returns
// auth.ErrDisplayNameTaken / auth.ErrEmailTaken on UNIQUE collisions.
func (s *PlayerStore) CreatePlayerByAdmin(
	ctx context.Context, displayName, email, passwordHash, role string,
) (*auth.Player, error) {
	cleanedDisplayName := strings.TrimSpace(displayName)
	cleanedEmail := strings.ToLower(strings.TrimSpace(email))
	row, err := s.q.CreatePlayerByAdmin(ctx, db.CreatePlayerByAdminParams{
		DisplayName:  cleanedDisplayName,
		Email:        sql.NullString{String: cleanedEmail, Valid: cleanedEmail != ""},
		PasswordHash: sql.NullString{String: passwordHash, Valid: passwordHash != ""},
		Role:         role,
	})
	if err != nil {
		return nil, s.classifyCredentialConflict(ctx, cleanedDisplayName, cleanedEmail, err)
	}

	return playerFromRow(row), nil
}

// InsertAdminAudit records one admin action against a target player
// (#450). payload is a pre-serialised JSON blob; the writer is
// responsible for the schema. Errors wrap through so the handler can
// log them without obscuring the SQL error.
func (s *PlayerStore) InsertAdminAudit(
	ctx context.Context, actorPlayerID, targetPlayerID int64, action, payload string,
) error {
	if err := s.q.InsertAdminAudit(ctx, db.InsertAdminAuditParams{
		ActorPlayerID:  sql.NullInt64{Int64: actorPlayerID, Valid: true},
		TargetPlayerID: targetPlayerID,
		Action:         action,
		Payload:        payload,
	}); err != nil {
		return fmt.Errorf("failed to insert admin audit: %w", err)
	}

	return nil
}

// SetPlayerRole sets the role on the row identified by id (#538). role is one
// of auth.RolePlayer / auth.RoleHost / auth.RoleAdmin; role_changed_at is
// stamped to the current time. Returns auth.ErrPlayerNotFound when no row
// matches.
func (s *PlayerStore) SetPlayerRole(ctx context.Context, playerID int64, role string) error {
	rows, err := s.q.SetPlayerRole(ctx, db.SetPlayerRoleParams{
		Role: role,
		ID:   playerID,
	})
	if err != nil {
		return fmt.Errorf("failed to set role: %w", err)
	}
	if rows == 0 {
		return auth.ErrPlayerNotFound
	}

	return nil
}

// DemoteAdmin moves the Admin row identified by id to the supplied non-admin
// role, refusing the change atomically when the row is the only remaining Admin
// (#997). The guarded UPDATE and the admin-count test run in one statement so
// two concurrent demotions of the two remaining admins cannot both pass and
// leave zero admins. A zero-row result is classified inside the same
// transaction: ErrLastAdmin when the row is still Admin (so only the
// last-admin clause blocked it), otherwise ErrPlayerNotFound (the id matches no
// row, or a row that is no longer Admin).
func (s *PlayerStore) DemoteAdmin(ctx context.Context, playerID int64, role string) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		rows, err := q.DemoteAdminGuarded(ctx, db.DemoteAdminGuardedParams{
			Role: role,
			ID:   playerID,
		})
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		if rows > 0 {
			return nil
		}

		return classifyDemoteRefusal(ctx, q, playerID)
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrLastAdmin):
		return auth.ErrLastAdmin
	case errors.Is(err, auth.ErrPlayerNotFound):
		return auth.ErrPlayerNotFound
	default:
		return fmt.Errorf("demote admin: %w", err)
	}
}

// classifyDemoteRefusal explains a zero-row DemoteAdminGuarded result inside the
// same transaction: ErrLastAdmin when the row is still Admin (so only the
// last-admin clause blocked it), otherwise ErrPlayerNotFound (the id matches no
// row, or a row that is no longer Admin).
func classifyDemoteRefusal(ctx context.Context, q *db.Queries, playerID int64) error {
	player, err := q.GetPlayer(ctx, playerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrPlayerNotFound
		}

		return fmt.Errorf("classify demote refusal: %w", err)
	}
	if player.Role == auth.RoleAdmin {
		return auth.ErrLastAdmin
	}

	return auth.ErrPlayerNotFound
}

// HasAnyAdmin reports whether any player currently holds the admin role.
// Backs the first-boot bootstrap that mints an admin from env vars only when
// none exists yet.
func (s *PlayerStore) HasAnyAdmin(ctx context.Context) (bool, error) {
	exists, err := s.q.HasAnyAdmin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check for any admin: %w", err)
	}

	return exists, nil
}

// ListAdmins returns every current Admin ordered by displayName (#320/#538).
// Empty slice when no Admin exists yet.
func (s *PlayerStore) ListAdmins(ctx context.Context) ([]*auth.AdminEntry, error) {
	rows, err := s.q.ListAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list admins: %w", err)
	}

	out := make([]*auth.AdminEntry, 0, len(rows))
	for _, r := range rows {
		entry := &auth.AdminEntry{
			ID:          r.ID,
			DisplayName: r.DisplayName,
			Email:       r.Email.String,
		}
		if r.RoleChangedAt.Valid {
			changed := r.RoleChangedAt.Time
			entry.RoleChangedAt = &changed
		}
		out = append(out, entry)
	}

	return out, nil
}

// ListAdminAuditForTarget returns the most-recent admin actions taken
// against the supplied target player, newest-first. Empty slice when
// no audit rows exist; the detail view renders an empty-state line in
// that case.
func (s *PlayerStore) ListAdminAuditForTarget(
	ctx context.Context, targetPlayerID, limit int64,
) ([]*auth.AdminAuditEntry, error) {
	rows, err := s.q.ListAdminAuditForTarget(ctx, db.ListAdminAuditForTargetParams{
		TargetPlayerID: targetPlayerID,
		RowLimit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list admin audit for target: %w", err)
	}

	out := make([]*auth.AdminAuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, &auth.AdminAuditEntry{
			ID:               r.ID,
			ActorPlayerID:    r.ActorPlayerID.Int64,
			ActorDisplayName: r.ActorDisplayName,
			TargetPlayerID:   r.TargetPlayerID,
			Action:           r.Action,
			Payload:          r.Payload,
			CreatedAt:        r.CreatedAt,
		})
	}

	return out, nil
}

// parseSQLiteTimestamp parses the bare SQLite datetime format
// ("YYYY-MM-DD HH:MM:SS") that modernc.org/sqlite returns for an
// aggregate over a DATETIME column whose values were written by
// CURRENT_TIMESTAMP. An unparseable value falls through to nil so a
// single malformed row does not fail the whole admin list page.
func parseSQLiteTimestamp(raw string) *time.Time {
	const sqliteDateTime = "2006-01-02 15:04:05"
	if t, err := time.Parse(sqliteDateTime, raw); err == nil {
		return &t
	}

	return nil
}

// classifyUpdateDisplayNameErr maps an UpdatePlayerDisplayName storage error
// onto the auth-package sentinels. [sql.ErrNoRows] from the UPDATE is ambiguous
// (the id might not exist OR the row already carries a password_hash), so it
// re-queries by id to disambiguate.
func (s *PlayerStore) classifyUpdateDisplayNameErr(ctx context.Context, playerID int64, err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return auth.ErrDisplayNameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to update player display name: %w", err)
	}

	if _, getErr := s.q.GetPlayer(ctx, playerID); getErr != nil {
		if errors.Is(getErr, sql.ErrNoRows) {
			return auth.ErrPlayerNotFound
		}

		return fmt.Errorf("failed to verify player after display name update: %w", getErr)
	}

	return auth.ErrPlayerNotAnonymous
}

// classifyClaimErr maps a ClaimPlayer storage error onto the auth-package
// sentinels. [sql.ErrNoRows] from the UPDATE is ambiguous (the id might
// not exist OR the row has already been claimed), so it re-queries by id
// to disambiguate.
func (s *PlayerStore) classifyClaimErr(
	ctx context.Context, playerID int64, cleanedDisplayName, cleanedEmail string, err error,
) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return s.classifyCredentialConflict(ctx, cleanedDisplayName, cleanedEmail, err)
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

// classifyCredentialConflict maps a SQLITE UNIQUE violation onto
// ErrDisplayNameTaken or ErrEmailTaken by looking up which column the
// caller already took. Any other error wraps through unchanged.
func (s *PlayerStore) classifyCredentialConflict(
	ctx context.Context, cleanedDisplayName, cleanedEmail string, err error,
) error {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return fmt.Errorf("failed to create player: %w", err)
	}
	if _, lookupErr := s.q.GetPlayerByDisplayName(ctx, cleanedDisplayName); lookupErr == nil {
		return auth.ErrDisplayNameTaken
	}
	if cleanedEmail != "" {
		if _, lookupErr := s.q.GetPlayerByEmail(
			ctx,
			sql.NullString{String: cleanedEmail, Valid: true},
		); lookupErr == nil {
			return auth.ErrEmailTaken
		}
	}

	return fmt.Errorf("failed to create player: %w", err)
}

func playerFromRow(row db.Player) *auth.Player {
	p := &auth.Player{
		ID:                 row.ID,
		DisplayName:        row.DisplayName,
		Role:               row.Role,
		CreatedAt:          row.CreatedAt,
		DisplayNameClaimed: row.DisplayNameClaimed != 0,
		SessionVersion:     row.SessionVersion,
	}
	if row.Email.Valid {
		p.Email = row.Email.String
	}
	if row.PasswordHash.Valid {
		p.PasswordHash = row.PasswordHash.String
	}
	if row.EmailVerifiedAt.Valid {
		verified := row.EmailVerifiedAt.Time
		p.EmailVerifiedAt = &verified
	}
	if row.ApprovedAt.Valid {
		approved := row.ApprovedAt.Time
		p.ApprovedAt = &approved
	}

	return p
}
