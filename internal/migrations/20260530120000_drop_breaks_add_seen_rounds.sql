-- +goose Up
-- #444: drive play and admin off question groups instead of breaks. The
-- breaks feature is removed entirely: per-game round-summary acknowledgement
-- now tracks rounds via game_seen_rounds, mirroring the shape
-- game_seen_breaks had. Breaks content and seen-state are dropped; no data
-- is migrated (settled decision in #444). game_seen_breaks is a child of
-- breaks, so dropping the child first and then the parent needs no table
-- rebuild and no foreign_keys=OFF dance.
-- +goose StatementBegin
CREATE TABLE game_seen_rounds
(
    game_id  VARCHAR(20) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id INTEGER     NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    seen_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (game_id, round_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE game_seen_breaks;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX breaks_quiz_position_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE breaks;
-- +goose StatementEnd

-- +goose Down
-- Recreate breaks and game_seen_breaks exactly as migrations
-- 20260525000000 and 20260525120000 left them, then drop game_seen_rounds.
-- Down does not restore any dropped break rows; it only restores the schema.
-- +goose StatementBegin
CREATE TABLE breaks
(
    id         INTEGER PRIMARY KEY,
    quiz_id    INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    position   INTEGER  NOT NULL,
    text       TEXT     NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX breaks_quiz_position_idx ON breaks(quiz_id, position);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE game_seen_breaks
(
    game_id  VARCHAR(20) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    break_id INTEGER     NOT NULL REFERENCES breaks (id) ON DELETE CASCADE,
    seen_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (game_id, break_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE game_seen_rounds;
-- +goose StatementEnd
