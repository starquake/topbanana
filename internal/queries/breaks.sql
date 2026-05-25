-- name: GetBreak :one
SELECT *
FROM breaks
WHERE id = ?
LIMIT 1;

-- name: ListBreaksByQuiz :many
SELECT *
FROM breaks
WHERE quiz_id = ?
ORDER BY position;

-- name: CreateBreak :one
INSERT INTO breaks (quiz_id, text, position)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateBreak :execresult
UPDATE breaks
SET text       = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteBreak :execresult
DELETE FROM breaks
WHERE id = ?;

-- name: NextBreakPosition :one
-- Returns the highest position in use for the given quiz, or 0 when the
-- quiz has no breaks yet. The CAST + COALESCE forces sqlc to type the
-- result as int64 instead of interface{} (raw MAX can return NULL).
-- Callers add 1 to get the next-position to assign on a new break.
-- Mirrors MaxQuestionPosition (#352).
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) AS max_position
FROM breaks
WHERE quiz_id = ?;
