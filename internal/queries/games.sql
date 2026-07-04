-- name: GetGame :one
SELECT *
FROM games
WHERE id = ?;

-- name: CreateGame :one
-- is_preview marks an owner preview game that stays off the leaderboard and play_count (#1192).
INSERT INTO games (id, quiz_id, is_preview)
VALUES (?, ?, ?)
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
-- UNIQUE(game_id, player_id, game_question_id) constraint serves as
-- the index for the game_id filter.
SELECT *
FROM game_answers
WHERE game_id = ?
ORDER BY game_question_id;

-- name: CreateGameQuestion :one
-- started_at and expired_at are bound as CURRENT_TIMESTAMP-format text strings
-- ('YYYY-MM-DD HH:MM:SS') via the CAST, so the stored values land in the exact
-- UTC encoding the leaderboard staleness comparison in
-- ListParticipantsForQuizLeaderboard reads. Binding a Go time.Time would arrive
-- in the driver's t.String() format ('... -0700 MST'); the timezone-offset
-- suffix makes the lexical compare invert across a DST boundary and flip the
-- in-progress dot (#789). The store binds value.UTC().Format(...) so both the
-- stored column and the bound cutoff share one encoding.
-- ON CONFLICT DO NOTHING: the UNIQUE INDEX on (game_id, question_id) prevents
-- double-issuance when two concurrent /next calls race. A conflict yields
-- sql.ErrNoRows; the store fetches the existing row via
-- GetGameQuestionByGameAndQuestion and returns ErrQuestionAlreadyIssued so the
-- service treats it as a resume.
INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
VALUES (?, ?, CAST(sqlc.arg('started_at') AS TEXT), CAST(sqlc.arg('expired_at') AS TEXT))
ON CONFLICT (game_id, question_id) DO NOTHING
RETURNING *;

-- name: GetGameQuestionByGameAndQuestion :one
-- Fetches an existing game_questions row by (game_id, question_id). Used by
-- the store's CreateQuestion to recover the winning row when ON CONFLICT DO
-- NOTHING yields no rows (a concurrent /next race).
SELECT *
FROM game_questions
WHERE game_id = ? AND question_id = ?;

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
       p.display_name           AS display_name,
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
WHERE g.quiz_id = ?
  AND g.is_preview = 0;

-- name: ListParticipantsForQuizLeaderboard :many
-- One row per player joined to the quiz, flagged with is_completed
-- (every quiz question issued) and is_stale (#336: latest
-- game_question unanswered and expired before stale_before).
-- Canonical entry set per #335 so a joined-but-unanswered player
-- still appears at 0.
-- Joins through `games` so the WHERE filters on games.quiz_id (NOT
-- NULL); game_participants.quiz_id is nullable in the schema, which
-- would otherwise force sqlc to infer sql.NullInt64 for the parameter.
-- stale_before is bound as a CURRENT_TIMESTAMP-format text string
-- ('YYYY-MM-DD HH:MM:SS') via the CAST so it shares the UTC encoding
-- expired_at is stored in (see CreateGameQuestion); a bound Go time.Time
-- would arrive in t.String() format whose timezone-offset suffix inverts
-- the lexical compare across a DST boundary and flips the in-progress dot (#789).
SELECT gp.player_id AS player_id,
       p.display_name   AS display_name,
       CASE WHEN (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id) > 0
             AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = gp.game_id) >=
                 (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
            THEN 1 ELSE 0 END AS is_completed,
       CASE WHEN EXISTS (
              SELECT 1 FROM game_questions gq
              WHERE gq.game_id = gp.game_id
                AND gq.expired_at < CAST(sqlc.arg('stale_before') AS TEXT)
                AND NOT EXISTS (SELECT 1 FROM game_answers ga WHERE ga.game_question_id = gq.id)
                AND NOT EXISTS (SELECT 1 FROM game_questions gqn WHERE gqn.game_id = gp.game_id AND gqn.id > gq.id)
            ) THEN 1 ELSE 0 END AS is_stale
FROM game_participants gp
         JOIN games g   ON g.id = gp.game_id
         JOIN players p ON p.id = gp.player_id
WHERE g.quiz_id = sqlc.arg('quiz_id')
  AND g.is_preview = 0;

-- name: GetGameByPlayerAndQuiz :one
-- Returns the most-recent game for the given (player, quiz) pair. Used by the
-- player-side resume flow (GET /api/quizzes/{slugID}/my-game) and as a
-- defensive backstop in CreateGame so the same player cannot start a second
-- attempt at a quiz they have already played.
SELECT g.id, g.quiz_id, g.created_at, g.started_at, g.is_preview
FROM games g
         JOIN game_participants gp ON gp.game_id = g.id
WHERE gp.player_id = ?
  AND g.quiz_id = ?
ORDER BY g.created_at DESC
LIMIT 1;

-- name: GetRealGameByPlayerAndQuiz :one
-- Returns the most-recent non-preview game for the (player, quiz) pair, so the
-- resume flow skips a stale owner-preview and the owner can still record a real run (#1192).
SELECT g.id, g.quiz_id, g.created_at, g.started_at, g.is_preview
FROM games g
         JOIN game_participants gp ON gp.game_id = g.id
WHERE gp.player_id = ?
  AND g.quiz_id = ?
  AND g.is_preview = 0
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

-- name: MarkRoundSeen :exec
-- Records that the player has acknowledged the given phase of the round
-- boundary in the given game (#548). ON CONFLICT DO NOTHING makes the
-- POST /rounds/{id}/seen/{phase} endpoint idempotent: a second call
-- returns 204 without bumping seen_at or inserting a duplicate row.
INSERT INTO game_seen_rounds (game_id, round_id, phase)
VALUES (?, ?, ?)
ON CONFLICT (game_id, round_id, phase) DO NOTHING;

-- name: ListSeenRoundPhasesByGame :many
-- Lists the (round_id, phase) pairs the player has already acknowledged
-- in the given game. The round-walking iterator in game.Service.GetNext
-- uses the result set to skip past acknowledged round boundary phases
-- (#548).
SELECT round_id, phase
FROM game_seen_rounds
WHERE game_id = ?;

-- name: ReattributeGameAnswers :execrows
-- Re-assigns game_answers rows from from_player_id to to_player_id for
-- the games belonging to from_player_id whose quizzes the destination
-- player has not yet played. Skipping the conflict cases keeps the
-- UNIQUE INDEX on game_participants (player_id, quiz_id) satisfied
-- when ReattributeGameParticipants runs immediately after.
--
-- Used by the post-login migration (#406) that carries an anonymous
-- visitor's game history onto the account they just signed into.
-- Returns the affected row count so the caller can decide whether to
-- broadcast an SSE refresh or skip the no-op case.
--
-- The two queries are called inside a single transaction at the
-- service layer so a partial migration cannot leave answers
-- attributed to a player who has no participant row for that game.
UPDATE game_answers
SET player_id = sqlc.arg('to_player_id')
WHERE game_answers.player_id = sqlc.arg('from_player_id')
  AND game_answers.game_id IN (
      SELECT gp.game_id FROM game_participants gp
      WHERE gp.player_id = sqlc.arg('from_player_id')
        AND NOT EXISTS (
            SELECT 1 FROM game_participants gp2
            WHERE gp2.player_id = sqlc.arg('to_player_id')
              AND gp2.quiz_id = gp.quiz_id
        )
  );

-- name: ReattributeGameParticipants :execrows
-- Re-assigns the participant rows themselves, skipping any quiz the
-- destination player has already played (UNIQUE (player_id, quiz_id)
-- would otherwise reject the UPDATE). Run after
-- ReattributeGameAnswers inside the same transaction so the answers
-- are moved while the source participation still exists.
UPDATE game_participants
SET player_id = sqlc.arg('to_player_id')
WHERE game_participants.player_id = sqlc.arg('from_player_id')
  AND NOT EXISTS (
      SELECT 1 FROM game_participants gp2
      WHERE gp2.player_id = sqlc.arg('to_player_id')
        AND gp2.quiz_id = game_participants.quiz_id
  );
