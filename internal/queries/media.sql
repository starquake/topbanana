-- name: CreateMedia :one
-- Inserts a per-quiz media row (#936) and returns it with the assigned id and
-- created_at. The bytes already live on disk under the per-quiz directory; this
-- row records the relative path plus the metadata the pipeline computed
-- (dimensions, byte size, sha256 of the stored image). thumb_path is nullable
-- because a non-image type (later) may not pre-generate a thumbnail.
--
-- The row is inserted not-ready (ready = 0): a later MarkMediaReady flips it
-- once the files are written and the paths recorded (#992). Until then the
-- library list hides it, so an upload cancelled before the flip never appears,
-- and the not-ready sweep drops the stranded row plus its files.
INSERT INTO media (
    quiz_id, type, mime, path, thumb_path,
    width, height, size_bytes, sha256, duration_ms, created_by_player_id, ready
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
    sqlc.arg('duration_ms'),
    sqlc.arg('created_by_player_id'),
    0
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

-- name: MarkMediaReady :execresult
-- Flips a media row ready (#992): the final step of the two-phase upload, run
-- only after the files are on disk and the paths recorded. Until this lands the
-- library list hides the row, so a cancel before this flip leaves nothing the
-- host can see. The caller checks RowsAffected to confirm the row still exists.
UPDATE media
SET ready = 1
WHERE id = sqlc.arg('id');

-- name: GetMedia :one
-- Returns a single media row by id. sql.ErrNoRows means the id does not name a
-- row; the store maps that to media.ErrMediaNotFound.
SELECT *
FROM media
WHERE id = sqlc.arg('id');

-- name: ListMediaByQuiz :many
-- Returns every ready media row scoped to a quiz, newest first, for the
-- per-quiz library grid. Ordered by id DESC as a stable tiebreaker since
-- created_at has second resolution and a bulk upload can share a timestamp.
-- ready = 1 hides a row whose upload was cancelled after the paths committed
-- but before the ready flip, so a cancelled upload never shows in the library
-- (#992).
SELECT *
FROM media
WHERE quiz_id = sqlc.arg('quiz_id')
  AND ready = 1
ORDER BY created_at DESC, id DESC;

-- name: DeleteMedia :execresult
-- Deletes a media row by id. The caller checks RowsAffected to distinguish a
-- real delete from a no-op (missing id), and unlinks the files separately
-- (best-effort) so a desync between row and file is reconciled by the cleanup
-- tooling rather than failing the delete.
DELETE FROM media
WHERE id = sqlc.arg('id');

-- name: CountMediaByQuiz :one
-- Returns the number of ready media rows for a quiz. Not-ready rows (in-flight
-- or stranded uploads) are excluded so the count reflects only finished library
-- images, matching what ListMediaByQuiz shows (#992).
SELECT count(*) AS count
FROM media
WHERE quiz_id = sqlc.arg('quiz_id')
  AND ready = 1;

-- name: ListStaleNotReadyMedia :many
-- Lists not-ready media rows older than the cutoff for the in-flight-upload
-- sweep (#992). A row stays not-ready only between CreateMedia and the final
-- MarkMediaReady; the sole way one lingers past that brief window is a request
-- whose context was cancelled mid-upload, leaving a committed-but-hidden row.
-- The cutoff is computed in SQL (datetime('now', '-<seconds> seconds')) so both
-- sides of the comparison are SQLite text in the CURRENT_TIMESTAMP encoding
-- rows are minted with, not a cross-format Go time.Time comparison. path and
-- thumb_path come back so the sweep can unlink the files before deleting the
-- row.
SELECT id, path, thumb_path
FROM media
WHERE ready = 0
  AND created_at < datetime('now', '-' || CAST(sqlc.arg('seconds') AS INTEGER) || ' seconds')
ORDER BY id;
