-- name: GetRound :one
SELECT *
FROM rounds
WHERE id = ?
LIMIT 1;

-- name: ListRoundsByQuiz :many
SELECT *
FROM rounds
WHERE quiz_id = ?
ORDER BY position;

-- name: GetDefaultRound :one
-- Returns the lowest-position round for a quiz. Every quiz has at least
-- one round (the default 'Round 1' created by migration
-- 20260530000000), so question-insert paths resolve the round to attach
-- a new question to via this query until slice 3 adds real round
-- selection.
SELECT *
FROM rounds
WHERE quiz_id = ?
ORDER BY position
LIMIT 1;

-- name: CreateRound :one
INSERT INTO rounds (quiz_id, position, title, summary, boundary_duration_seconds)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRound :execresult
UPDATE rounds
SET title      = ?,
    summary = ?,
    position   = ?,
    boundary_duration_seconds = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateRoundPosition :execresult
-- Position-only update used by the per-row up/down reorder buttons.
-- Mirrors UpdateBreakPosition so the move path can rewrite a single
-- column without touching title/summary or updated_at - the reorder
-- is not a content edit.
UPDATE rounds
SET position = ?
WHERE id = ?;

-- name: DeleteRound :execresult
DELETE FROM rounds
WHERE id = ?;
