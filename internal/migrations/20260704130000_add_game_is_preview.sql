-- +goose Up
-- +goose StatementBegin
-- is_preview marks a host preview game (#1192): the owner test-plays a draft
-- solo quiz to verify it without their run reaching the quiz leaderboard or
-- bumping the durable play_count. Defaults 0 so every real game counts. An
-- in-place constant-default ADD COLUMN, so no FK rebuild even though games is
-- a parent table.
ALTER TABLE games ADD COLUMN is_preview INTEGER NOT NULL DEFAULT 0
    CHECK (is_preview IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE games DROP COLUMN is_preview;
-- +goose StatementEnd
