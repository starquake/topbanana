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
-- Selects the per-answer scoring inputs for every completed game of the
-- given quiz. A game counts as complete when every quiz question has
-- been issued, i.e. the count of game_questions rows for the game has
-- caught up with the count of questions on the quiz. Partial games
-- (where the player walked away mid-quiz) are filtered out so the
-- leaderboard only compares finishers. The one-attempt-per-(player,
-- quiz) constraint enforced by #145 keeps a player from showing up more
-- than once.
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
WHERE g.quiz_id = ?
  AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id);

-- name: GetGameByPlayerAndQuiz :one
-- Returns the most-recent game for the given (player, quiz) pair. Used by the
-- player-side resume flow (GET /api/quizzes/{slugID}/my-game) and as a
-- defensive backstop in CreateGame so the same player cannot start a second
-- attempt at a quiz they have already played.
SELECT g.id, g.quiz_id, g.created_at, g.started_at
FROM games g
         JOIN game_participants gp ON gp.game_id = g.id
WHERE gp.player_id = ?
  AND g.quiz_id = ?
ORDER BY g.created_at DESC
LIMIT 1;

-- name: ListQuizIDsForPlayer :many
-- Lists distinct quiz IDs where the given player has at least one
-- recorded answer. The claim-name flow uses this to know which
-- leaderboard SSE streams to repaint when a player updates their
-- display name: every quiz they appear on gets a fresh snapshot
-- pushed to its subscribers. Filtering on game_answers rather than
-- game_participants means we skip quizzes the player joined but
-- never actually answered on, which would not show up on the
-- leaderboard anyway.
SELECT DISTINCT g.quiz_id
FROM game_answers ga
         JOIN games g ON g.id = ga.game_id
WHERE ga.player_id = ?;

-- name: ListGameIDsForPlayerOnQuiz :many
-- Lists every game ID the player has on the given quiz. The reset flow
-- collects these once at the start of the in-Go transaction and feeds them
-- into the dependent deletes; doing it that way avoids a delete-order
-- problem where each subsequent statement's subquery would see fewer rows
-- as we drained the dependency tables.
SELECT g.id
FROM games g
         JOIN game_participants gp ON gp.game_id = g.id
WHERE gp.player_id = ?
  AND g.quiz_id = ?;

-- name: ListGameIDsForQuiz :many
-- Lists every game ID for the quiz, regardless of player. Used by the
-- quiz delete flow so the in-Go transaction can drop dependent
-- game_answers / game_questions / game_participants / games rows before
-- the quiz row itself is deleted (questions and options cascade from
-- the quiz, but the game_* tables do not).
SELECT id
FROM games
WHERE quiz_id = ?;

-- name: DeleteGameAnswersByGameIDs :exec
-- Hard-deletes every game_answers row attached to the given game IDs. Used
-- by the player-on-quiz reset flow with the IDs gathered up front by
-- ListGameIDsForPlayerOnQuiz.
DELETE
FROM game_answers
WHERE game_id IN (sqlc.slice('game_ids'));

-- name: ListGameQuestionIDsForQuestion :many
-- Lists every game_questions.id where the given question has been issued.
-- Used by the question delete flow so the in-Go transaction can drop
-- dependent game_answers rows scoped to THIS question's game_question rows
-- before deleting the game_questions and the question itself. Snapshotted
-- up front so subsequent deletes do not see a moving target as rows drain.
SELECT id
FROM game_questions
WHERE question_id = ?;

-- name: DeleteGameAnswersByGameQuestionIDs :exec
-- Hard-deletes game_answers rows that reference the given game_question
-- IDs. Filtering by game_question_id (not game_id) is deliberate: a single
-- game can hold answers for many questions, and the question delete must
-- only wipe the answers tied to the question being deleted, leaving the
-- other questions in the same game untouched.
DELETE
FROM game_answers
WHERE game_question_id IN (sqlc.slice('game_question_ids'));

-- name: DeleteGameQuestionsByQuestionID :exec
-- Hard-deletes every game_questions row issued for the given question.
-- Once the dependent game_answers rows are gone (see
-- DeleteGameAnswersByGameQuestionIDs) the FK on game_answers.game_question_id
-- no longer blocks, so a single-arg delete by question_id is enough.
DELETE
FROM game_questions
WHERE question_id = ?;

-- name: DeleteGameQuestionsByGameIDs :exec
-- Hard-deletes every game_questions row attached to the given game IDs.
DELETE
FROM game_questions
WHERE game_id IN (sqlc.slice('game_ids'));

-- name: DeleteGameParticipantsByGameIDs :exec
-- Hard-deletes every game_participants row attached to the given game IDs.
DELETE
FROM game_participants
WHERE game_id IN (sqlc.slice('game_ids'));

-- name: DeleteGamesByIDs :exec
-- Hard-deletes the given games themselves. Run last; the participant /
-- question / answer rows referencing these games have already been removed.
DELETE
FROM games
WHERE id IN (sqlc.slice('ids'));
