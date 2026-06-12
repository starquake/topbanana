-- name: ListQuizzes :many
-- INNER JOIN on players so the admin list can render "Created by ..."
-- alongside each quiz without an N+1 lookup. created_by_player_id is
-- NOT NULL (since migration 20260520200000 / #281) and references
-- players(id) with no player-delete path, so the join always matches;
-- the previous LEFT JOIN + sql.NullString in the wrapper was dead
-- defensive code that masked a real invariant (#359). If a future
-- migration adds player deletion, restore the LEFT JOIN with an
-- explicit "Unknown" rendering instead of letting deleted-creator
-- quizzes silently render a blank "Created by" line.
--
-- visibility comes back unfiltered so the admin list can show every
-- row regardless of who can play it; the public-facing API filters
-- explicitly via ListPublicQuizzes (#103).
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       q.time_limit_seconds,
       q.visibility,
       q.mode,
       q.play_count,
       p.display_name AS created_by_display_name
FROM quizzes q
         JOIN players p ON p.id = q.created_by_player_id
ORDER BY q.updated_at DESC, q.id DESC;

-- name: ListPublicQuizzes :many
-- Public-facing variant of ListQuizzes (#103). Filters to visibility =
-- 'public' so unlisted and private quizzes never appear in the player
-- client's quiz picker or on the home page's all-quizzes view. The
-- mode = 'solo' filter (MP-0 / #677) keeps live (hosted-only) quizzes
-- out of the solo browse paths too.
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       q.time_limit_seconds,
       q.visibility,
       q.mode,
       q.play_count,
       p.display_name AS created_by_display_name
FROM quizzes q
         JOIN players p ON p.id = q.created_by_player_id
WHERE q.visibility = 'public'
  AND q.mode = 'solo'
ORDER BY q.updated_at DESC, q.id DESC;

-- name: ListLiveQuizzes :many
-- Live-mode variant of ListQuizzes (#836). Filters to mode = 'live' so the
-- host intermission picker only offers hostable quizzes. Visibility is left
-- unfiltered, matching CreateSession, which gates a host on mode = 'live'
-- alone (any live quiz is hostable, regardless of who created it).
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       q.time_limit_seconds,
       q.visibility,
       q.mode,
       q.play_count,
       p.display_name AS created_by_display_name
FROM quizzes q
         JOIN players p ON p.id = q.created_by_player_id
WHERE q.mode = 'live'
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
-- Same INNER JOIN as ListQuizzes so single-quiz fetches carry the
-- creator's display_name for the admin view's "Created by ..." line. See
-- ListQuizzes for why the LEFT JOIN was dropped (#359).
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       q.created_by_player_id,
       q.time_limit_seconds,
       q.visibility,
       q.mode,
       q.play_count,
       p.display_name AS created_by_display_name
FROM quizzes q
         JOIN players p ON p.id = q.created_by_player_id
WHERE q.id = ?
LIMIT 1;

-- name: QuizExists :one
-- Cheap existence check for a quiz by ID. Returns 1 when the row exists,
-- 0 otherwise. Used by handlers and services that need to validate the
-- quiz is real before doing further work but do not need the full tree
-- of questions and options that GetQuiz materialises.
SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?) AS quiz_exists;

-- name: GetQuizVisibility :one
-- Returns just the visibility column for a quiz. Used by the read-path
-- visibility gate, which only needs visibility + existence and must not
-- pay the questions/options fan-out that GetQuiz materialises.
SELECT visibility
FROM quizzes
WHERE id = ?
LIMIT 1;

-- name: CreateQuiz :one
-- created_by_player_id is NOT NULL with an FK to players.id (migration
-- 20260520200000 / #281). [QuizStore.CreateQuiz] short-circuits with
-- ErrCreatorRequired when the caller forgot to stamp the session
-- admin, so the FK constraint is the second line of defence.
INSERT INTO quizzes (title, slug, description, created_by_player_id, time_limit_seconds, visibility, mode, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
RETURNING *;

-- name: UpdateQuiz :execresult
UPDATE quizzes
SET title              = ?,
    slug               = ?,
    description        = ?,
    time_limit_seconds = ?,
    visibility         = ?,
    mode               = ?,
    updated_at         = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateQuizMode :execresult
-- Flips just the play mode without touching the question tree, so the
-- solo/live toggle (#830) cannot clobber a concurrent question edit the way
-- the full UpdateQuiz would.
UPDATE quizzes
SET mode       = ?,
    updated_at = CURRENT_TIMESTAMP
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

-- name: ListQuestionIDsByRoundID :many
-- Lists the question IDs attached to a round, snapshotted up front by the
-- round delete so it can clean up each question's dependent game_questions
-- and game_answers rows before dropping the round. questions.round_id has
-- ON DELETE CASCADE, but the played-game rows that reference those
-- questions do not, so the round delete must wipe them in FK order first.
SELECT id
FROM questions
WHERE round_id = ?
ORDER BY position;

-- name: CreateQuestion :one
INSERT INTO questions (quiz_id, round_id, text, position, image_url, time_limit_seconds)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateQuestion :execresult
UPDATE questions
SET text               = ?,
    position           = ?,
    image_url          = ?,
    time_limit_seconds = ?
WHERE id = ?;

-- name: MoveQuestionToRound :execresult
-- Reassigns a question to a different round within the same quiz (#444).
-- Position is unchanged - questions stay in quiz-wide position order, so
-- a round change is a single column rewrite.
UPDATE questions
SET round_id = ?
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

-- name: ListOptionsByQuizID :many
-- Returns every option for a quiz in one round-trip so callers can group
-- options by question in Go instead of running one query per question.
SELECT o.*
FROM options o
         JOIN questions q ON q.id = o.question_id
WHERE q.quiz_id = ?
ORDER BY o.question_id, o.id;

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

-- name: BumpQuizPlayCountForGame :exec
-- Increments the durable hit counter (#891) for the quiz that owns this solo
-- game. Resolves the quiz from the game id so the caller only signals "this
-- play completed" without threading the quiz id. Never decremented; the CHECK
-- on quizzes.play_count keeps it non-negative.
UPDATE quizzes
SET play_count = play_count + 1
WHERE quizzes.id = (SELECT quiz_id FROM games WHERE games.id = ?);

-- name: BumpQuizPlayCountForSession :exec
-- Increments the durable hit counter (#891) for the quiz a live session is
-- playing. Resolves the quiz from the session id. A session with a NULL
-- quiz_id (a quiz-less room) matches no quiz row, so the bump is a safe no-op.
UPDATE quizzes
SET play_count = play_count + 1
WHERE quizzes.id = (SELECT quiz_id FROM sessions WHERE sessions.id = ?);