-- +goose Up
-- #548: split the round boundary into two phases (intro before a round's
-- questions, results recap after them). Acknowledgement is now per-phase, so
-- game_seen_rounds gains a phase column ('intro' | 'results') and the primary
-- key becomes (game_id, round_id, phase). No seen-state is migrated: existing
-- in-flight games re-showing a boundary card once is acceptable (the #444
-- "no data migrated" precedent). game_seen_rounds is a child table (nothing
-- references it), so a defer_foreign_keys rebuild inside goose's default
-- transaction keeps FK enforcement on while postponing the check to COMMIT.
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE game_seen_rounds;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE game_seen_rounds
(
    game_id  VARCHAR(20) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id INTEGER     NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    phase    TEXT        NOT NULL CHECK (phase IN ('intro', 'results')),
    seen_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (game_id, round_id, phase)
);
-- +goose StatementEnd

-- +goose Down
-- Restore game_seen_rounds to its pre-#548 shape (PK without phase). No
-- seen-state is migrated back.
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE game_seen_rounds;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE game_seen_rounds
(
    game_id  VARCHAR(20) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id INTEGER     NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    seen_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (game_id, round_id)
);
-- +goose StatementEnd
