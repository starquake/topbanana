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

-- name: ListAnswersForQuizLeaderboard :many
-- Selects the per-answer scoring inputs for every game of the given quiz.
-- The leaderboard service assumes one attempt per (player, quiz) (#145 covers
-- enforcement) and sums these rows per player; if multiple attempts ever leak
-- through, scores will be inflated until enforcement lands.
SELECT ga.player_id        AS player_id,
       p.username           AS username,
       gq.started_at        AS question_started_at,
       gq.expired_at        AS question_expired_at,
       ga.answered_at       AS answered_at,
       o.is_correct         AS is_correct
FROM game_answers ga
         JOIN games g ON g.id = ga.game_id
         JOIN game_questions gq ON gq.id = ga.game_question_id
         JOIN options o ON o.id = ga.option_id
         JOIN players p ON p.id = ga.player_id
WHERE g.quiz_id = ?;
