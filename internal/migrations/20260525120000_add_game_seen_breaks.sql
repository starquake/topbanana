-- +goose Up
-- +goose StatementBegin
-- #167 slice 2: per-game record of which breaks the player has already
-- passed through. The composite PK on (game_id, break_id) gives us the
-- "already seen" check for the merged iterator and a free no-op on
-- repeated POST /breaks/{id}/seen calls (ON CONFLICT DO NOTHING). Both
-- FKs cascade so a quiz or game delete sweeps the rows.
CREATE TABLE game_seen_breaks
(
    game_id  VARCHAR(20) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    break_id INTEGER     NOT NULL REFERENCES breaks (id) ON DELETE CASCADE,
    seen_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (game_id, break_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE game_seen_breaks;
-- +goose StatementEnd
