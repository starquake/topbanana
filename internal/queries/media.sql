-- name: CreateMedia :one
-- Inserts a per-quiz media row (#936) and returns it with the assigned id and
-- created_at. The bytes already live on disk under the per-quiz directory; this
-- row records the relative path plus the metadata the pipeline computed
-- (dimensions, byte size, sha256 of the stored image). thumb_path is nullable
-- because a non-image type (later) may not pre-generate a thumbnail.
INSERT INTO media (
    quiz_id, type, mime, path, thumb_path,
    width, height, size_bytes, sha256, created_by_player_id
)
VALUES (
    sqlc.arg('quiz_id'),
    sqlc.arg('type'),
    sqlc.arg('mime'),
    sqlc.arg('path'),
    sqlc.arg('thumb_path'),
    sqlc.arg('width'),
    sqlc.arg('height'),
    sqlc.arg('size_bytes'),
    sqlc.arg('sha256'),
    sqlc.arg('created_by_player_id')
)
RETURNING *;

-- name: UpdateMediaPaths :execresult
-- Sets the on-disk paths of a media row after the files are written. The row is
-- inserted first to assign the id the filenames embed (<quizID>/<id>.jpg), so
-- the paths cannot be known at insert time; this second write fills them in.
-- The caller checks RowsAffected to confirm the row still exists.
UPDATE media
SET path = sqlc.arg('path'),
    thumb_path = sqlc.arg('thumb_path')
WHERE id = sqlc.arg('id');

-- name: GetMedia :one
-- Returns a single media row by id. sql.ErrNoRows means the id does not name a
-- row; the store maps that to media.ErrMediaNotFound.
SELECT *
FROM media
WHERE id = sqlc.arg('id');

-- name: ListMediaByQuiz :many
-- Returns every media row scoped to a quiz, newest first, for the per-quiz
-- library grid. Ordered by id DESC as a stable tiebreaker since created_at has
-- second resolution and a bulk upload can share a timestamp.
SELECT *
FROM media
WHERE quiz_id = sqlc.arg('quiz_id')
ORDER BY created_at DESC, id DESC;

-- name: DeleteMedia :execresult
-- Deletes a media row by id. The caller checks RowsAffected to distinguish a
-- real delete from a no-op (missing id), and unlinks the files separately
-- (best-effort) so a desync between row and file is reconciled by the cleanup
-- tooling rather than failing the delete.
DELETE FROM media
WHERE id = sqlc.arg('id');

-- name: CountMediaByQuiz :one
-- Returns the number of media rows for a quiz, for an upload-count guard and
-- the library header.
SELECT count(*) AS count
FROM media
WHERE quiz_id = sqlc.arg('quiz_id');
