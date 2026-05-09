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
INSERT INTO players (username, password_hash, role)
VALUES (
    sqlc.arg('username'),
    sqlc.arg('password_hash'),
    CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN (SELECT COUNT(*) FROM players WHERE password_hash IS NOT NULL) = 0 THEN 'admin'
        ELSE 'player'
    END
)
RETURNING *;

-- name: CreateAnonymousPlayer :one
-- Used by the EnsurePlayer middleware to back a fresh visitor with a real
-- players row before they can play. email and password_hash are NULL; role
-- is fixed to 'player' because the "first password-bearing registrant
-- becomes admin" SQL above filters by password_hash IS NOT NULL, so an
-- anonymous row never qualifies for promotion.
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
UPDATE players
SET username = sqlc.arg('username'),
    password_hash = sqlc.arg('password_hash'),
    role = CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN (SELECT COUNT(*) FROM players AS pp WHERE pp.password_hash IS NOT NULL) = 0 THEN 'admin'
        ELSE 'player'
    END
WHERE players.id = sqlc.arg('id')
  AND players.password_hash IS NULL
RETURNING *;

-- name: SetPlayerPasswordHash :execrows
-- Used by the cmd/server -reset-password operator tool to rotate a single
-- player's password without disturbing username / role / email. Returns the
-- number of affected rows so the caller can map "no rows" to a "username not
-- found" error.
UPDATE players
SET password_hash = sqlc.arg('password_hash')
WHERE username = sqlc.arg('username');
