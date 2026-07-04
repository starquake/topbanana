-- +goose Up
-- +goose StatementBegin
-- is_preview marks an owner preview game that stays off the leaderboard and play_count; defaults 0 so every real game counts (#1192).
-- Constant-default ADD COLUMN is in-place in SQLite, so no FK rebuild despite games being a parent table.
ALTER TABLE games ADD COLUMN is_preview INTEGER NOT NULL DEFAULT 0
    CHECK (is_preview IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE games DROP COLUMN is_preview;
-- +goose StatementEnd
