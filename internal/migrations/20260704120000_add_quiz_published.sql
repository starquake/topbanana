-- +goose Up
-- +goose StatementBegin
-- published marks whether a quiz is playable by real players; drafts (0) are owner-preview-only and editable (#1192).
-- Constant-default ADD COLUMN is in-place in SQLite, so no FK rebuild despite quizzes being a parent table.
ALTER TABLE quizzes ADD COLUMN published INTEGER NOT NULL DEFAULT 0
    CHECK (published IN (0, 1));
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill existing quizzes to published so nothing already playable stops working (#1192).
UPDATE quizzes SET published = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN published;
-- +goose StatementEnd
