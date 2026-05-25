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

-- name: UpdateBreakPosition :execresult
-- Position-only update used by the per-row up/down reorder buttons on
-- the admin quiz view. Mirrors UpdateQuestionPosition so the move
-- path can rewrite a single column without touching text or
-- updated_at - the admin reorder is not a content edit.
UPDATE breaks
SET position = ?
WHERE id = ?;

-- name: DeleteBreak :execresult
DELETE FROM breaks
WHERE id = ?;
