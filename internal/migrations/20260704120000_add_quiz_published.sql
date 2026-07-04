-- +goose Up
-- +goose StatementBegin
-- published marks whether a quiz is finished and playable by real players
-- (#1192): a quiz starts as a draft (published=0), previewable only by its
-- owner, and is locked from edits once published. A nullable-free ADD COLUMN
-- with a constant DEFAULT is an in-place change in SQLite, so there is no
-- FK-rebuild dance here even though quizzes is a parent table.
ALTER TABLE quizzes ADD COLUMN published INTEGER NOT NULL DEFAULT 0
    CHECK (published IN (0, 1));
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill every existing quiz to published so nothing that was already live
-- (all quizzes were playable before this migration) stops working. New quizzes
-- default 0 (draft).
UPDATE quizzes SET published = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN published;
-- +goose StatementEnd
