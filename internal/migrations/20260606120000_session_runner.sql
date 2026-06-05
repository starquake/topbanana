-- NO TRANSACTION required: this rebuilds the sessions table (a PARENT of
-- session_players and the new session_answers) to widen the phase CHECK from
-- the lobby-only set to the full runner state machine and to add the runner's
-- timing columns. SQLite ignores PRAGMA foreign_keys inside a transaction, and
-- PRAGMA defer_foreign_keys is not enough on a parent rebuild: dropping the
-- parent invalidates the child rows' references in a way the deferred check at
-- COMMIT still trips on (verified against modernc.org/sqlite v1.31.x, see
-- 20260529160000). So this uses the grandfathered foreign_keys = OFF pattern
-- inside an explicit transaction with the _fk_guard CHECK to abort on a
-- dangling reference.
-- +goose NO TRANSACTION

-- +goose Up
-- The runner advances a session through round_intro -> question -> reveal per
-- question and ends at finished. current_round_id / current_question_id point
-- at the question being run; question_started_at / question_expires_at are the
-- server-authoritative answer window (the same StartedAt/ExpiredAt the solo
-- game uses) so clients drive their countdowns off the server clock.
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

-- +goose StatementBegin
INSERT INTO sessions_new (id, quiz_id, host_player_id, join_code, phase, created_at, started_at, finished_at)
SELECT id, quiz_id, host_player_id, join_code, phase, created_at, started_at, finished_at
FROM sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sessions_new RENAME TO sessions;
-- +goose StatementEnd

-- session_answers records one pick per (session, question, player). option_id
-- is the chosen option; answered_at is the server timestamp the pick landed
-- (used by CalculateScore against the question window). score is filled in when
-- the question closes - NULL until then, so a read before close never leaks a
-- score. UNIQUE (session_id, question_id, player_id) makes a re-submit for the
-- same question a no-op the runner can ignore.
-- +goose StatementBegin
CREATE TABLE session_answers
(
    id          INTEGER PRIMARY KEY,
    session_id  TEXT     NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    question_id INTEGER  NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    player_id   INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    option_id   INTEGER  NOT NULL REFERENCES options (id) ON DELETE CASCADE,
    answered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    score       INTEGER,
    UNIQUE (session_id, question_id, player_id)
);
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
DROP TABLE session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sessions_old
(
    id             TEXT PRIMARY KEY,
    quiz_id        INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code      TEXT     NOT NULL UNIQUE,
    phase          TEXT     NOT NULL DEFAULT 'lobby' CHECK (phase IN ('lobby')),
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at     DATETIME,
    finished_at    DATETIME
);
-- +goose StatementEnd

-- The Up widened phase and added gameplay columns; rolling back drops both, so
-- any session past the lobby is coerced back to 'lobby' to satisfy the narrow
-- CHECK on the old table.
-- +goose StatementBegin
INSERT INTO sessions_old (id, quiz_id, host_player_id, join_code, phase, created_at, started_at, finished_at)
SELECT id, quiz_id, host_player_id, join_code, 'lobby', created_at, started_at, finished_at
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
