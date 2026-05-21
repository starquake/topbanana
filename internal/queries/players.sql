-- name: GetPlayerByUsername :one
SELECT *
FROM players
WHERE username = ?
LIMIT 1;

-- name: CreatePlayerWithCredentials :one
-- The role decision lives in SQL so the "first password-bearing registrant
-- becomes admin" rule is atomic. Two concurrent first-registrations would
-- both observe count == 0 if we computed the role in Go and called INSERT
-- separately, leaving us with two admins. Folding the check into the same
-- INSERT serialises the decision against the row that gets written.
--
-- The third placeholder is the role requested by the caller (env-list match,
-- otherwise "player"). If "admin" is requested explicitly we honour that;
-- otherwise we promote when there are no other rows with a password_hash
-- (legacy seed admin without a password is intentionally ignored).
--
-- username_claimed is set to 1 because a registering user explicitly chose
-- their username at the register form. The column tracks "did the player
-- pick this name themselves" (vs auto-generated petname), so a fresh
-- registrant must be marked as claimed from the moment the row is written.
INSERT INTO players (username, password_hash, role, username_claimed)
VALUES (
    sqlc.arg('username'),
    sqlc.arg('password_hash'),
    CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN (SELECT COUNT(*) FROM players WHERE password_hash IS NOT NULL) = 0 THEN 'admin'
        ELSE 'player'
    END,
    1
)
RETURNING *;

-- name: CreateAnonymousPlayer :one
-- Used by the EnsurePlayer middleware to back a fresh visitor with a real
-- players row before they can play. email and password_hash are NULL; role
-- is fixed to 'player' because the "first password-bearing registrant
-- becomes admin" SQL above filters by password_hash IS NOT NULL, so an
-- anonymous row never qualifies for promotion.
--
-- username_claimed defaults to 0: the auto-generated petname is not a name
-- the visitor picked, so the row is unclaimed until they rename via the
-- PATCH /api/players/me endpoint or sign up through ClaimPlayer below.
INSERT INTO players (username, role)
VALUES (sqlc.arg('username'), 'player')
RETURNING *;

-- name: ClaimPlayer :one
-- Upgrades an anonymous (password_hash IS NULL) row in place so that an
-- already-playing visitor keeps their player_id when they sign up. The
-- WHERE password_hash IS NULL guard makes this idempotent: a second claim
-- attempt against an already-credentialled row returns no rows, which the
-- store maps to ErrPlayerAlreadyClaimed.
--
-- The role CASE mirrors CreatePlayerWithCredentials so the "first
-- password-bearing registrant becomes admin" rule still triggers when the
-- very first sign-up happens through the claim path (i.e. the registrant
-- played anonymously first). The subquery aliases the players table as pp
-- so the column reference in the WHERE is unambiguous against the row
-- being updated.
--
-- username_claimed is set to 1 because the visitor is explicitly choosing
-- their username via the register form. This is the register-after-playing
-- path: the row now represents a player who picked their own name, so it
-- must look identical to a CreatePlayerWithCredentials row to downstream
-- callers.
UPDATE players
SET username = sqlc.arg('username'),
    password_hash = sqlc.arg('password_hash'),
    role = CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN (SELECT COUNT(*) FROM players AS pp WHERE pp.password_hash IS NOT NULL) = 0 THEN 'admin'
        ELSE 'player'
    END,
    username_claimed = 1
WHERE players.id = sqlc.arg('id')
  AND players.password_hash IS NULL
RETURNING *;

-- name: SetPlayerPasswordHash :execrows
-- Used by the cmd/server -reset-password operator tool to rotate a single
-- player's password without disturbing username / role / email. Returns the
-- number of affected rows so the caller can map "no rows" to a "username not
-- found" error.
--
-- username_claimed is set to 1 alongside the password because once an
-- operator has set a password on a row, the username is no longer an
-- auto-assigned petname the player should be nudged to replace (#289). The
-- migration 20260511120000 ran the same backfill at the time, but only for
-- rows that already had a password_hash; later password sets via this
-- query previously left username_claimed at 0, which made the seed
-- admin (id=1) keep popping the claim-name modal in the player client.
UPDATE players
SET password_hash    = sqlc.arg('password_hash'),
    username_claimed = 1
WHERE username = sqlc.arg('username');

-- name: UpdatePlayerUsername :one
-- Updates the username on an anonymous player row in place. The WHERE
-- clause refuses the update when the player has already claimed a
-- non-anonymous identity (password_hash IS NOT NULL), so the SQL is the
-- atomic guard against a stale anonymous check in the service layer.
-- Returns the updated row when one was affected; the wrapper distinguishes
-- "not anonymous anymore" (sql.ErrNoRows) from "username collision"
-- (UNIQUE constraint failure on players.username).
--
-- username_claimed is set to 1 because this is the dedicated claim-name
-- endpoint (PATCH /api/players/me); the visitor is explicitly picking
-- their display name. After this update the row reads as "player chose
-- this name" identically to the credentialled-registration path.
UPDATE players
SET username = sqlc.arg('username'),
    username_claimed = 1
WHERE id = sqlc.arg('id') AND password_hash IS NULL
RETURNING *;
