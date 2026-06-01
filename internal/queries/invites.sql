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

-- name: ListPendingInvites :many
-- Lists every still-pending invite for the admin management view (#318),
-- newest first. LEFT JOIN players surfaces the inviter's display_name for the
-- "invited by X" column; inviter_display_name is NULL when the invite carries
-- no actor (invited_by_player_id NULL) or the inviting admin's row has since
-- been deleted (ON DELETE SET NULL). Includes still-expired-but-not-swept
-- rows so the list matches what the sweep has actually pruned; the template
-- surfaces expires_at so the admin can tell a stale link apart.
SELECT
    invites.id,
    invites.email,
    invites.invited_by_player_id,
    players.display_name AS inviter_display_name,
    invites.created_at,
    invites.expires_at
FROM invites
LEFT JOIN players ON players.id = invites.invited_by_player_id
WHERE invites.status = 'pending'
ORDER BY invites.created_at DESC, invites.id DESC;

-- name: RevokeInvite :one
-- Marks a pending invite revoked so its link stops resolving (#318). Only a
-- pending row moves; an already-accepted, already-revoked, or non-existent id
-- matches nothing and returns sql.ErrNoRows, which the handler maps to a clear
-- "no longer pending" flash rather than a 500. RETURNING id confirms exactly
-- one row changed.
UPDATE invites
SET status = 'revoked'
WHERE id = sqlc.arg('id')
  AND status = 'pending'
RETURNING id;

-- name: RotateInviteToken :one
-- Resend path (#318): overwrites a pending invite's token hash and expiry with
-- a freshly minted pair. The new hash makes the new link live; the old hash no
-- longer matches any row, so the previously emailed link is dead (UNIQUE
-- token_hash means the new value cannot collide). Only a pending row moves; a
-- non-pending or non-existent id returns sql.ErrNoRows. RETURNING email lets
-- the handler dispatch the new link without a second read.
UPDATE invites
SET token_hash = sqlc.arg('token_hash'),
    expires_at = sqlc.arg('expires_at')
WHERE id = sqlc.arg('id')
  AND status = 'pending'
RETURNING email;
