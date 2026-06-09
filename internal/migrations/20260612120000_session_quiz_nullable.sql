-- NO TRANSACTION required: this rebuilds the sessions table (a PARENT of
-- session_players and session_answers) to make quiz_id nullable. SQLite cannot
-- drop a NOT NULL constraint in place, so the column change needs a table
-- rebuild. SQLite ignores PRAGMA foreign_keys inside a transaction, and PRAGMA
-- defer_foreign_keys is not enough on a parent rebuild: dropping the parent
-- invalidates the child rows' references in a way the deferred check at COMMIT
-- still trips on (verified against modernc.org/sqlite v1.31.x, see
-- 20260529160000 and 20260611120000). So this uses the grandfathered
-- foreign_keys = OFF pattern inside an explicit transaction with the _fk_guard
-- CHECK to abort on a dangling reference.
-- +goose NO TRANSACTION

-- +goose Up
-- A room with no current quiz (quiz_id NULL) is the "no game running yet" state
-- (#836): a host opens a room up front, players join, and the host picks the
-- first live quiz ad-hoc. quiz_id becomes nullable so that staging state is a
-- valid row; the FK to quizzes (ON DELETE CASCADE) is unchanged, so a deleted
-- quiz still cascades to its sessions when one is set. Every other column,
-- constraint, and default (game_seq, the intermission phase, the runner
-- columns) is preserved verbatim from 20260611120000.
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
    quiz_id             INTEGER REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id      INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code           TEXT     NOT NULL UNIQUE,
    phase               TEXT     NOT NULL DEFAULT 'lobby'
        CHECK (phase IN ('lobby', 'round_intro', 'question', 'reveal',
                         'round_results', 'intermission', 'finished')),
    game_seq            INTEGER  NOT NULL DEFAULT 1,
    current_round_id    INTEGER REFERENCES rounds (id) ON DELETE SET NULL,
    current_question_id INTEGER REFERENCES questions (id) ON DELETE SET NULL,
    question_started_at DATETIME,
    question_expires_at DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at          DATETIME,
    finished_at         DATETIME,
    host_last_seen_at   DATETIME,
    start_at            DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO sessions_new (id, quiz_id, host_player_id, join_code, phase, game_seq, current_round_id,
                          current_question_id, question_started_at, question_expires_at, created_at, started_at,
                          finished_at, host_last_seen_at, start_at)
SELECT id,
       quiz_id,
       host_player_id,
       join_code,
       phase,
       game_seq,
       current_round_id,
       current_question_id,
       question_started_at,
       question_expires_at,
       created_at,
       started_at,
       finished_at,
       host_last_seen_at,
       start_at
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

-- Rolling back restores the NOT NULL quiz_id. A room created without a quiz
-- (quiz_id NULL) cannot exist under the old schema, so those rooms are dropped
-- here rather than coerced to a bogus quiz id (there is no sane quiz to point a
-- quiz-less staging room at). This is lossy by design: a quiz-less room is a
-- brand-new state with no pre-rooms equivalent. Foreign keys are OFF for this
-- rebuild, so the ON DELETE CASCADE does NOT fire: the child rows
-- (session_players, session_answers) of a quiz-less room must be deleted
-- explicitly first, or they would be orphaned and trip the _fk_guard at COMMIT.
-- +goose StatementBegin
DELETE FROM session_answers WHERE session_id IN (SELECT id FROM sessions WHERE quiz_id IS NULL);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM session_players WHERE session_id IN (SELECT id FROM sessions WHERE quiz_id IS NULL);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM sessions WHERE quiz_id IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sessions_old
(
    id                  TEXT PRIMARY KEY,
    quiz_id             INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id      INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code           TEXT     NOT NULL UNIQUE,
    phase               TEXT     NOT NULL DEFAULT 'lobby'
        CHECK (phase IN ('lobby', 'round_intro', 'question', 'reveal',
                         'round_results', 'intermission', 'finished')),
    game_seq            INTEGER  NOT NULL DEFAULT 1,
    current_round_id    INTEGER REFERENCES rounds (id) ON DELETE SET NULL,
    current_question_id INTEGER REFERENCES questions (id) ON DELETE SET NULL,
    question_started_at DATETIME,
    question_expires_at DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at          DATETIME,
    finished_at         DATETIME,
    host_last_seen_at   DATETIME,
    start_at            DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO sessions_old (id, quiz_id, host_player_id, join_code, phase, game_seq, current_round_id,
                          current_question_id, question_started_at, question_expires_at, created_at, started_at,
                          finished_at, host_last_seen_at, start_at)
SELECT id,
       quiz_id,
       host_player_id,
       join_code,
       phase,
       game_seq,
       current_round_id,
       current_question_id,
       question_started_at,
       question_expires_at,
       created_at,
       started_at,
       finished_at,
       host_last_seen_at,
       start_at
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
