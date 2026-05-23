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
-- quiz_id is denormalised onto game_participants so the UNIQUE INDEX
-- on (player_id, quiz_id) can enforce the one-attempt-per-(player, quiz)
-- rule at the DB level (#273). Callers populate it from games.quiz_id
-- inside the same Service.CreateGame call.
INSERT INTO game_participants (game_id, player_id, quiz_id)
VALUES (?, ?, ?)
RETURNING *;

-- name: CreateAnswer :one
-- answered_at is passed in from the handler instead of being SQLite's
-- CURRENT_TIMESTAMP (#237). The handler accepts the client's tappedAt
-- and clamps it to [question.started_at, time.Now()] before this
-- INSERT runs, so an honest player on a slow link gets the network
-- latency refunded instead of being scored late, and a malicious or
-- clock-skewed client can't claim a time outside that window.
INSERT INTO game_answers (game_id, player_id, game_question_id, option_id, answered_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPlayer :one
SELECT *
FROM players
WHERE id = ?;

-- name: ListGameQuestionsByGameID :many
-- Ordered by id so callers can rely on the last element being the
-- most recently issued question. GetNextQuestion's resume path needs
-- that to decide whether to return the in-flight question or advance.
SELECT *
FROM game_questions
WHERE game_id = ?
ORDER BY id;

-- name: ListAnswersByGameQuestionID :many
SELECT *
FROM game_answers
WHERE game_question_id = ?;

-- name: ListAnswersByGameID :many
-- Returns every game_answer for a given game, ordered by
-- game_question_id so callers can partition rows per question in a
-- single pass. Replaces the N+1 pattern of calling
-- ListAnswersByGameQuestionID once per issued question (#356); the
-- game_id column is already covered by the FK's implicit index.
SELECT *
FROM game_answers
WHERE game_id = ?
ORDER BY game_question_id;

-- name: CreateGameQuestion :one
INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListAnswersForQuizLeaderboard :many
-- Selects the per-answer scoring inputs for every game of the given
-- quiz (finished AND in-progress, #244). The completed-only filter
-- previously hid mid-quiz players from the leaderboard; the live
-- leaderboard now needs them so the host (and the player themselves)
-- can watch the standings move in real time.
--
-- is_completed carries the same finisher predicate that used to live
-- in the WHERE clause: a game counts as complete when every quiz
-- question has been issued (game_questions rows >= quiz questions
-- count). The Go layer collapses one row per (player, game) into a
-- single LeaderboardEntry with the per-player Completed flag.
SELECT ga.player_id        AS player_id,
       p.username           AS username,
       gq.started_at        AS question_started_at,
       gq.expired_at        AS question_expired_at,
       ga.answered_at       AS answered_at,
       o.is_correct         AS is_correct,
       CASE WHEN (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id) > 0
             AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = g.id) >=
                 (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
            THEN 1 ELSE 0 END AS is_completed
FROM game_answers ga
         JOIN games g ON g.id = ga.game_id
         JOIN game_questions gq ON gq.id = ga.game_question_id
         JOIN options o ON o.id = ga.option_id
         JOIN players p ON p.id = ga.player_id
WHERE g.quiz_id = ?;

-- name: ListParticipantsForQuizLeaderboard :many
-- Lists every player who has joined a game for the given quiz, with the
-- same is_completed predicate ListAnswersForQuizLeaderboard carries on
-- the answer rows. The Service uses this list as the canonical set of
-- leaderboard entries (#335) so a player who clicked Start but has not
-- yet submitted an answer still appears with a 0 score and the
-- in-progress dot, instead of being invisible until their first answer
-- commits. The answers query then contributes the per-row scoring
-- inputs used to roll up each entry's running total.
-- Joins through `games` so the WHERE filters on games.quiz_id (NOT
-- NULL); game_participants.quiz_id is nullable in the schema, which
-- would otherwise force sqlc to infer sql.NullInt64 for the parameter.
SELECT gp.player_id AS player_id,
       p.username   AS username,
       CASE WHEN (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id) > 0
             AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = gp.game_id) >=
                 (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
            THEN 1 ELSE 0 END AS is_completed
FROM game_participants gp
         JOIN games g   ON g.id = gp.game_id
         JOIN players p ON p.id = gp.player_id
WHERE g.quiz_id = ?;

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
-- Lists distinct quiz IDs the given player has joined. The claim-name
-- flow uses this to know which leaderboard SSE streams to repaint
-- when a player updates their display name: every quiz they appear
-- on gets a fresh snapshot pushed to its subscribers.
--
-- Post-#335 the live leaderboard surfaces a player as soon as they
-- create a game (before any answer commits), so the fan-out must
-- read from game_participants -- filtering on game_answers would
-- skip joined-but-unanswered quizzes and leave their subscribers
-- stuck on the stale auto-petname (#354). quiz_id became NOT NULL
-- in migration 20260524200000 (#357), so no Valid-guard is needed.
SELECT DISTINCT gp.quiz_id
FROM game_participants gp
WHERE gp.player_id = ?;

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
