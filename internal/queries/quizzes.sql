-- name: ListQuizzes :many
-- LEFT JOIN on players so the admin list can render "Created by ..."
-- alongside each quiz without an N+1 lookup. Every quiz has a creator
-- (NOT NULL since migration 20260520200000 / #281); the JOIN tolerates
-- a deleted player row by surfacing created_by_username NULL, so the
-- store decodes that field via sql.NullString.
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       p.username AS created_by_username
FROM quizzes q
         LEFT JOIN players p ON p.id = q.created_by_player_id
ORDER BY q.updated_at DESC, q.id DESC;

-- name: QuestionCountsByQuiz :many
-- Returns one row per quiz that has at least one question. Quizzes with
-- zero questions are absent; callers should treat a missing entry as 0.
-- Used by the admin list to render "{N} questions" alongside ListQuizzes
-- without coupling the count into the Quiz domain type.
SELECT quiz_id, COUNT(*) AS question_count
FROM questions
GROUP BY quiz_id;

-- name: GetQuiz :one
-- Same LEFT JOIN as ListQuizzes so single-quiz fetches carry the
-- creator's username for the admin view's "Created by ..." line.
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       p.username AS created_by_username
FROM quizzes q
         LEFT JOIN players p ON p.id = q.created_by_player_id
WHERE q.id = ?
LIMIT 1;

-- name: QuizExists :one
-- Cheap existence check for a quiz by ID. Returns 1 when the row exists,
-- 0 otherwise. Used by handlers and services that need to validate the
-- quiz is real before doing further work but do not need the full tree
-- of questions and options that GetQuiz materialises.
SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?) AS quiz_exists;

-- name: CreateQuiz :one
-- created_by_player_id is NOT NULL with an FK to players.id (migration
-- 20260520200000 / #281). [QuizStore.CreateQuiz] short-circuits with
-- ErrCreatorRequired when the caller forgot to stamp the session
-- admin, so the FK constraint is the second line of defence.
INSERT INTO quizzes (title, slug, description, created_by_player_id, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
RETURNING *;

-- name: UpdateQuiz :execresult
UPDATE quizzes
SET title       = ?,
    slug        = ?,
    description = ?,
    updated_at  = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteQuiz :execresult
DELETE
FROM quizzes
WHERE id = ?;

-- name: GetQuestion :one
SELECT *
FROM questions
WHERE id = ?
LIMIT 1;

-- name: ListQuestionsByQuizID :many
SELECT *
FROM questions
WHERE quiz_id = ?
ORDER BY position;

-- name: ListQuestionIDsByQuizID :many
SELECT id
FROM questions
WHERE quiz_id = ?
ORDER BY position;

-- name: CreateQuestion :one
INSERT INTO questions (quiz_id, text, position, image_url)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: UpdateQuestion :execresult
UPDATE questions
SET text = ?,
    position = ?,
    image_url = ?
WHERE id = ?;

-- name: UpdateQuestionPosition :execresult
-- Position-only update. Used by the reorder flow (#16) to swap a pair
-- of questions atomically inside a transaction without rewriting the
-- text/image fields.
UPDATE questions
SET position = ?
WHERE id = ?;

-- name: MaxQuestionPosition :one
-- Returns the highest position in use for the given quiz, or 0 when the
-- quiz has no questions yet. The CAST + COALESCE forces sqlc to type
-- the result as int64 instead of interface{} (raw MAX can return NULL).
-- Callers add 1 to get the next-position to assign on a new question.
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) AS max_position
FROM questions
WHERE quiz_id = ?;

-- name: DeleteQuestion :execresult
DELETE FROM questions
WHERE id = ?;

-- name: ListOptionsByQuestionID :many
SELECT *
FROM options
WHERE question_id = ?;

-- name: ListOptionIDsByQuestionID :many
SELECT id
FROM options
WHERE question_id = ?;

-- name: GetOption :one
SELECT *
FROM options
WHERE id = ?
LIMIT 1;

-- name: GetOptionsByIDs :many
SELECT *
FROM options
WHERE id IN (sqlc.slice('ids'));

-- name: CreateOption :one
INSERT INTO options (question_id, text, is_correct)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateOption :execresult
UPDATE options
SET text = ?,
    is_correct = ?
WHERE id = ?;

-- name: DeleteOption :execresult
DELETE FROM options
WHERE id = ?;