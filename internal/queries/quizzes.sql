-- name: ListQuizzes :many
SELECT *
FROM quizzes
ORDER BY updated_at DESC, id DESC;

-- name: QuestionCountsByQuiz :many
-- Returns one row per quiz that has at least one question. Quizzes with
-- zero questions are absent; callers should treat a missing entry as 0.
-- Used by the admin list to render "{N} questions" alongside ListQuizzes
-- without coupling the count into the Quiz domain type.
SELECT quiz_id, COUNT(*) AS question_count
FROM questions
GROUP BY quiz_id;

-- name: GetQuiz :one
SELECT *
FROM quizzes
WHERE id = ?
LIMIT 1;

-- name: CreateQuiz :one
INSERT INTO quizzes (title, slug, description, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
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