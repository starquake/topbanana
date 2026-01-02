-- name: ListQuizzes :many
SELECT *
FROM quizzes
ORDER BY id;

-- name: GetQuiz :one
SELECT *
FROM quizzes
WHERE id = ?
LIMIT 1;

-- name: CreateQuiz :one
INSERT INTO quizzes (title, slug, description)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateQuiz :execresult
UPDATE quizzes
SET title       = ?,
    slug        = ?,
    description = ?
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
INSERT INTO questions (quiz_id, text, position)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateQuestion :execresult
UPDATE questions
SET text = ?,
    position = ?
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