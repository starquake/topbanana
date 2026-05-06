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
