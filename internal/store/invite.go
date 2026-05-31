package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/db"
)

// CreateInvite inserts a row in invites with the given email, token hash,
// optional note, audit actor, and absolute expiry (#318). expiresAt is
// normalised to UTC so the driver's RFC3339 encoding lines up
// lexicographically with the UTC clock the consume/sweep paths read -
// mixing offsets between insert and read silently breaks the string
// comparison. An invitedByPlayerID of 0 stores NULL (deleted/unknown
// actor); an empty note stores NULL.
func (s *PlayerStore) CreateInvite(
	ctx context.Context, email, tokenHash, note string, invitedByPlayerID int64, expiresAt time.Time,
) error {
	if err := s.q.CreateInvite(ctx, db.CreateInviteParams{
		Email:             email,
		InvitedByPlayerID: sql.NullInt64{Int64: invitedByPlayerID, Valid: invitedByPlayerID != 0},
		TokenHash:         tokenHash,
		Note:              sql.NullString{String: note, Valid: note != ""},
		ExpiresAt:         expiresAt.UTC(),
	}); err != nil {
		return fmt.Errorf("failed to create invite: %w", err)
	}

	return nil
}

// GetLiveInvite returns the pending, unexpired invite matching the token
// hash, or auth.ErrInviteInvalid when no acceptable row matches (consumed,
// revoked, expired, or never existed). UTC on the wire so the expires_at
// comparison stays lexicographically sane regardless of the host timezone.
func (s *PlayerStore) GetLiveInvite(ctx context.Context, tokenHash string) (*auth.LiveInvite, error) {
	row, err := s.q.GetLiveInviteByTokenHash(ctx, db.GetLiveInviteByTokenHashParams{
		TokenHash: tokenHash,
		Now:       time.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrInviteInvalid
		}

		return nil, fmt.Errorf("failed to get live invite: %w", err)
	}

	return &auth.LiveInvite{
		ID:                row.ID,
		Email:             row.Email,
		InvitedByPlayerID: row.InvitedByPlayerID.Int64,
	}, nil
}

// ConsumeInvite atomically marks the invite accepted and stamps
// accepted_at. Returns auth.ErrInviteInvalid when no live row matches
// (never existed, expired, already accepted, or revoked). UTC on the wire
// for the same lexicographic reason as the create/lookup paths.
func (s *PlayerStore) ConsumeInvite(ctx context.Context, tokenHash string) error {
	now := time.Now().UTC()
	_, err := s.q.ConsumeInvite(ctx, db.ConsumeInviteParams{
		AcceptedAt: sql.NullTime{Time: now, Valid: true},
		TokenHash:  tokenHash,
		Now:        now,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrInviteInvalid
		}

		return fmt.Errorf("failed to consume invite: %w", err)
	}

	return nil
}

// DeleteExpiredInvites drops still-pending expired rows from invites.
// UTC mirrors the verify/reset sweeps so the lexicographic comparison
// stays consistent across the host timezone. Accepted and revoked rows
// are kept as an audit trail.
func (s *PlayerStore) DeleteExpiredInvites(ctx context.Context) error {
	if err := s.q.DeleteExpiredInvites(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to delete expired invites: %w", err)
	}

	return nil
}

// ListPendingInvites returns every still-pending invite, newest first,
// for the admin management view (#318). The inviter username is resolved
// via a LEFT JOIN; a NULL (no actor or deleted admin) maps to an empty
// string so the template renders a neutral dash instead of a Go zero
// value sneaking through.
func (s *PlayerStore) ListPendingInvites(ctx context.Context) ([]*auth.PendingInvite, error) {
	rows, err := s.q.ListPendingInvites(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending invites: %w", err)
	}

	invites := make([]*auth.PendingInvite, 0, len(rows))
	for _, row := range rows {
		invites = append(invites, &auth.PendingInvite{
			ID:                row.ID,
			Email:             row.Email,
			InvitedByPlayerID: row.InvitedByPlayerID.Int64,
			InviterUsername:   row.InviterUsername.String,
			CreatedAt:         row.CreatedAt,
			ExpiresAt:         row.ExpiresAt,
		})
	}

	return invites, nil
}

// RevokeInvite marks a pending invite revoked. Returns
// auth.ErrInviteNotPending when the id does not name a still-pending row
// (already accepted/revoked, or never existed) so the handler can flash a
// clear message instead of surfacing a 500.
func (s *PlayerStore) RevokeInvite(ctx context.Context, id int64) error {
	if _, err := s.q.RevokeInvite(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrInviteNotPending
		}

		return fmt.Errorf("failed to revoke invite: %w", err)
	}

	return nil
}

// RotateInviteToken overwrites a pending invite's token hash and expiry
// (the resend path), returning the invite's email so the caller can
// dispatch the new link. expiresAt is normalised to UTC for the same
// lexicographic reason as CreateInvite. Returns auth.ErrInviteNotPending
// when the id does not name a still-pending row.
func (s *PlayerStore) RotateInviteToken(
	ctx context.Context, id int64, newTokenHash string, newExpiresAt time.Time,
) (string, error) {
	email, err := s.q.RotateInviteToken(ctx, db.RotateInviteTokenParams{
		TokenHash: newTokenHash,
		ExpiresAt: newExpiresAt.UTC(),
		ID:        id,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", auth.ErrInviteNotPending
		}

		return "", fmt.Errorf("failed to rotate invite token: %w", err)
	}

	return email, nil
}
