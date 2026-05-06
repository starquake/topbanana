-- name: CountPlayers :one
-- CountPlayers counts only players that have registered through the form (password_hash IS NOT NULL).
-- This intentionally ignores legacy seed rows so the "first registered user becomes admin" rule still works.
SELECT COUNT(*) AS count
FROM players
WHERE password_hash IS NOT NULL;

-- name: GetPlayerByUsername :one
SELECT *
FROM players
WHERE username = ?
LIMIT 1;

-- name: CreatePlayerWithCredentials :one
INSERT INTO players (username, password_hash, role)
VALUES (?, ?, ?)
RETURNING *;
