-- +goose Up
-- +goose StatementBegin
-- #103: quiz visibility. NOT NULL with DEFAULT 'public' so existing
-- rows stay accessible to everyone (least-surprise default, per the
-- ticket's open-questions resolution). The CHECK enforces the same set
-- the admin form's selector exposes; any other value is rejected at
-- write time rather than silently surfacing later as a 500.
ALTER TABLE quizzes ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
    CHECK (visibility IN ('public', 'unlisted', 'private'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN visibility;
-- +goose StatementEnd
