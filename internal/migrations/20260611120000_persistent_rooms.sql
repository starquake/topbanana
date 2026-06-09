-- NO TRANSACTION required: this rebuilds the sessions table (a PARENT of
-- session_players and session_answers) to widen the phase CHECK with the new
-- intermission phase and to add game_seq. SQLite ignores PRAGMA foreign_keys
-- inside a transaction, and PRAGMA defer_foreign_keys is not enough on a parent
-- rebuild: dropping the parent invalidates the child rows' references in a way
-- the deferred check at COMMIT still trips on (verified against
-- modernc.org/sqlite v1.31.x, see 20260529160000). So this uses the
-- grandfathered foreign_keys = OFF pattern inside an explicit transaction with
-- the _fk_guard CHECK to abort on a dangling reference. session_answers (a
-- child) is rebuilt in the same transaction because FK enforcement is already
-- off here, so its game_seq column and widened unique key ride along.
-- +goose NO TRANSACTION

-- +goose Up
-- A session is becoming a persistent room that hosts a sequence of separately
-- scored games (#836). intermission sits between games: the just-finished game
-- shows its final standings while the host arms the next quiz, and the room
-- stays alive (it is not terminal like finished). game_seq counts which game in
-- the room is being played, starting at 1; it scopes every per-game answer read
-- so a re-run of the same quiz in a room does not blend scores across games.
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
INSERT INTO sessions_new (id, quiz_id, host_player_id, join_code, phase, current_round_id, current_question_id,
                          question_started_at, question_expires_at, created_at, started_at, finished_at,
                          host_last_seen_at, start_at)
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

-- session_answers gains game_seq (which game the pick belongs to) and widens its
-- unique key to include it, so re-running the same quiz in a room records a
-- fresh pick per game rather than colliding on the previous game's row. Existing
-- rows belong to game 1 (the only game pre-rooms), the column default.
-- +goose StatementBegin
CREATE TABLE session_answers_new
(
    id          INTEGER PRIMARY KEY,
    session_id  TEXT     NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    question_id INTEGER  NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    player_id   INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    option_id   INTEGER  NOT NULL REFERENCES options (id) ON DELETE CASCADE,
    answered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    score       INTEGER,
    game_seq    INTEGER  NOT NULL DEFAULT 1,
    UNIQUE (session_id, question_id, player_id, game_seq)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO session_answers_new (id, session_id, question_id, player_id, option_id, answered_at, score, game_seq)
SELECT id, session_id, question_id, player_id, option_id, answered_at, score, 1
FROM session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE session_answers_new RENAME TO session_answers;
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

-- The Up added game_seq and the intermission phase; rolling back drops both.
-- Only the latest game's answers survive (game_seq = the session's current
-- game_seq), because the old unique key (session_id, question_id, player_id)
-- cannot hold more than one game's picks for the same question/player; dropping
-- the others is the only way to satisfy it. A session sitting in intermission is
-- coerced back to 'finished' to satisfy the old narrower CHECK.
-- +goose StatementBegin
DELETE FROM session_answers
WHERE game_seq <> (SELECT s.game_seq FROM sessions s WHERE s.id = session_answers.session_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE session_answers_old
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

-- +goose StatementBegin
INSERT INTO session_answers_old (id, session_id, question_id, player_id, option_id, answered_at, score)
SELECT id, session_id, question_id, player_id, option_id, answered_at, score
FROM session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE session_answers_old RENAME TO session_answers;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE sessions_old
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
    finished_at         DATETIME,
    host_last_seen_at   DATETIME,
    start_at            DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO sessions_old (id, quiz_id, host_player_id, join_code, phase, current_round_id, current_question_id,
                          question_started_at, question_expires_at, created_at, started_at, finished_at,
                          host_last_seen_at, start_at)
SELECT id,
       quiz_id,
       host_player_id,
       join_code,
       CASE WHEN phase = 'intermission' THEN 'finished' ELSE phase END,
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
