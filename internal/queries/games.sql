-- name: GetGame :one
SELECT *
FROM games
WHERE id = ?;

-- name: CreateGame :one
INSERT INTO games (id, quiz_id)
VALUES (?, ?)
RETURNING *;

-- name: StartGame :execresult
UPDATE games
SET started_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ListParticipantsByGameID :many
SELECT *
FROM game_participants
WHERE game_id = ?;

-- name: CreateParticipant :one
INSERT INTO game_participants (game_id, player_id)
VALUES (?, ?)
RETURNING *;

-- name: CreateAnswer :one
INSERT INTO game_answers (game_id, player_id, game_question_id, option_id, answered_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
RETURNING *;

-- name: GetPlayer :one
SELECT *
FROM players
WHERE id = ?;

-- name: ListGameQuestionsByGameID :many
SELECT *
FROM game_questions
WHERE game_id = ?;

-- name: ListAnswersByGameQuestionID :many
SELECT *
FROM game_answers
WHERE game_question_id = ?;

-- name: CreateGameQuestion :one
INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
VALUES (?, ?, ?, ?)
RETURNING *;