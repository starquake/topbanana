-- name: CreateInvite :exec
-- Stores the sha256 hash of a freshly minted invite token (#318). The raw
-- token only exists on the way out the door in the email; a DB leak should
-- not be replayable against POST /accept-invite. invited_by_player_id is the
-- audit actor (the admin who sent the invite); it is nullable so a deleted
-- admin leaves the invite intact via ON DELETE SET NULL. note is an optional
-- free-text reminder the admin can attach.
INSERT INTO invites (email, invited_by_player_id, token_hash, note, expires_at)
VALUES (
    sqlc.arg('email'),
    sqlc.arg('invited_by_player_id'),
    sqlc.arg('token_hash'),
    sqlc.arg('note'),
    sqlc.arg('expires_at')
);

-- name: GetLiveInviteByTokenHash :one
-- Look up an invite by token hash, but only when it is still acceptable:
-- pending (not accepted, not revoked) and not expired. Used by the
-- GET /accept-invite preflight to short-circuit the form render for dead
-- links, and re-checked by POST. The caller passes 'now' so both sides of the
-- expires_at comparison use the driver's RFC3339 encoding (mixing time.Time
-- with CURRENT_TIMESTAMP produces strings of different lengths and the
-- lexicographic comparison silently lies). sql.ErrNoRows means consumed,
-- revoked, expired, or never existed; the caller maps that to a single
-- user-facing "invite link is no longer valid" response.
SELECT id, email, invited_by_player_id
FROM invites
WHERE token_hash = sqlc.arg('token_hash')
  AND status = 'pending'
  AND expires_at > sqlc.arg('now')
LIMIT 1;

-- name: ConsumeInvite :one
-- Atomic single-use consume: succeeds only when the row is still pending and
-- not expired. Marks the invite accepted and stamps accepted_at in one
-- statement so two concurrent accepts cannot both win. Returns the id so the
-- caller can confirm exactly one row moved. sql.ErrNoRows means the invite was
-- already accepted, revoked, expired, or never existed.
UPDATE invites
SET status = 'accepted',
    accepted_at = sqlc.arg('accepted_at')
WHERE token_hash = sqlc.arg('token_hash')
  AND status = 'pending'
  AND expires_at > sqlc.arg('now')
RETURNING id;

-- name: DeleteExpiredInvites :exec
-- Housekeeping for the startup + periodic sweep. Drops every still-pending
-- invite whose link has expired so the table does not grow without bound.
-- Accepted and revoked rows are kept as an audit trail. The caller passes
-- 'now' so the comparison runs in the driver's RFC3339 encoding.
DELETE FROM invites
WHERE status = 'pending'
  AND expires_at <= sqlc.arg('now');
