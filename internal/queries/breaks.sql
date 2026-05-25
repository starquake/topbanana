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
    position   = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteBreak :execresult
DELETE FROM breaks
WHERE id = ?;
