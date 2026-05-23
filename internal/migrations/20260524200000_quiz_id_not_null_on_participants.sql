-- +goose Up
-- +goose StatementBegin
-- #357: make game_participants.quiz_id NOT NULL. SQLite treats NULLs
-- as distinct in a UNIQUE constraint, so the (player_id, quiz_id)
-- UNIQUE INDEX added by 20260520180000 silently fails to enforce the
-- one-attempt-per-quiz rule the moment a NULL slips through. Today
-- CreateParticipant always sets Valid=true, but a future query that
-- omits the column or a backdoor INSERT would bypass the constraint
-- without a NOT NULL guard.
--
-- The backfill in 20260520180000 populated quiz_id from games.quiz_id
-- for every row, so the rebuild can assume the column is already
-- populated. The pre-flight check below aborts loudly if a NULL slips
-- through anyway so a botched data fix isn't quietly papered over.
--
-- Table rebuild instead of an ALTER COLUMN because SQLite doesn't
-- support ALTER COLUMN. defer_foreign_keys is the standard pattern
-- for any rebuild that crosses FK references (per CLAUDE.md
-- guidance). Goose wraps the whole migration in a transaction so a
-- NULL quiz_id in any pre-existing row would fail the INSERT below
-- with a NOT NULL constraint violation and roll the rebuild back —
-- the explicit RAISE pre-flight isn't available outside a trigger.
PRAGMA defer_foreign_keys = ON;

CREATE TABLE game_participants_new
(
    id        INTEGER PRIMARY KEY,
    game_id   VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id INTEGER     NOT NULL REFERENCES players (id),
    quiz_id   INTEGER     NOT NULL REFERENCES quizzes (id),
    joined_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO game_participants_new (id, game_id, player_id, quiz_id, joined_at)
SELECT id, game_id, player_id, quiz_id, joined_at
FROM game_participants;

DROP TABLE game_participants;
ALTER TABLE game_participants_new RENAME TO game_participants;

CREATE UNIQUE INDEX game_participants_player_quiz_idx
    ON game_participants (player_id, quiz_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;

DROP INDEX game_participants_player_quiz_idx;

CREATE TABLE game_participants_old
(
    id        INTEGER PRIMARY KEY,
    game_id   VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id INTEGER     NOT NULL REFERENCES players (id),
    quiz_id   INTEGER              REFERENCES quizzes (id),
    joined_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO game_participants_old (id, game_id, player_id, quiz_id, joined_at)
SELECT id, game_id, player_id, quiz_id, joined_at
FROM game_participants;

DROP TABLE game_participants;
ALTER TABLE game_participants_old RENAME TO game_participants;

CREATE UNIQUE INDEX game_participants_player_quiz_idx
    ON game_participants (player_id, quiz_id);
-- +goose StatementEnd
