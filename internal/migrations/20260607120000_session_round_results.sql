-- NO TRANSACTION required: this rebuilds the sessions table (a PARENT of
-- session_players and session_answers) to widen the phase CHECK with the new
-- round_results phase (MP-6 / #683), shown between rounds. SQLite ignores
-- PRAGMA foreign_keys inside a transaction, and PRAGMA defer_foreign_keys is
-- not enough on a parent rebuild: dropping the parent invalidates the child
-- rows' references in a way the deferred check at COMMIT still trips on
-- (verified against modernc.org/sqlite v1.31.x, see 20260529160000). So this
-- uses the grandfathered foreign_keys = OFF pattern inside an explicit
-- transaction with the _fk_guard CHECK to abort on a dangling reference.
-- +goose NO TRANSACTION

-- +goose Up
-- round_results sits after the last question of a round (before the next
-- round's intro) and exposes the per-player round delta + running total +
-- ranking the bar graph (MP-9) consumes. Only the phase CHECK changes; every
-- other column carries through the rebuild unchanged.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sessions_new
(
    id                  TEXT PRIMARY KEY,
    quiz_id             INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id      INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code           TEXT     NOT NULL UNIQUE,
    phase               TEXT     NOT NULL DEFAULT 'lobby'
        CHECK (phase IN ('lobby', 'round_intro', 'question', 'reveal', 'round_results', 'finished')),
    current_round_id    INTEGER REFERENCES rounds (id) ON DELETE SET NULL,
    current_question_id INTEGER REFERENCES questions (id) ON DELETE SET NULL,
    question_started_at DATETIME,
    question_expires_at DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at          DATETIME,
    finished_at         DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO sessions_new (id, quiz_id, host_player_id, join_code, phase, current_round_id, current_question_id,
                          question_started_at, question_expires_at, created_at, started_at, finished_at)
SELECT id,
       quiz_id,
       host_player_id,
       join_code,
       phase,
       current_round_id,
       current_question_id,
       question_started_at,
       question_expires_at,
       created_at,
       started_at,
       finished_at
FROM sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions_new RENAME TO sessions;
-- +goose StatementEnd

-- A bare PRAGMA foreign_key_check only RETURNS the violating rows; goose
-- discards that result set, so on its own it cannot stop a broken rebuild from
-- committing. This guard turns "a FK violation exists" into a failed INSERT
-- that aborts the whole transaction (and the migration).
-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sessions_old
(
    id                  TEXT PRIMARY KEY,
    quiz_id             INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id      INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code           TEXT     NOT NULL UNIQUE,
    phase               TEXT     NOT NULL DEFAULT 'lobby'
        CHECK (phase IN ('lobby', 'round_intro', 'question', 'reveal', 'finished')),
    current_round_id    INTEGER REFERENCES rounds (id) ON DELETE SET NULL,
    current_question_id INTEGER REFERENCES questions (id) ON DELETE SET NULL,
    question_started_at DATETIME,
    question_expires_at DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at          DATETIME,
    finished_at         DATETIME
);
-- +goose StatementEnd

-- The Up widened the CHECK with round_results; rolling back drops it, so any
-- session sitting in round_results is coerced back to 'reveal' (the phase it
-- entered round_results from) to satisfy the narrower CHECK on the old table.
-- +goose StatementBegin
INSERT INTO sessions_old (id, quiz_id, host_player_id, join_code, phase, current_round_id, current_question_id,
                          question_started_at, question_expires_at, created_at, started_at, finished_at)
SELECT id,
       quiz_id,
       host_player_id,
       join_code,
       CASE WHEN phase = 'round_results' THEN 'reveal' ELSE phase END,
       current_round_id,
       current_question_id,
       question_started_at,
       question_expires_at,
       created_at,
       started_at,
       finished_at
FROM sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions_old RENAME TO sessions;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
