-- +goose Up
-- +goose StatementBegin
-- language is an advisory content label (#1115): which language the quiz's
-- questions are written in ('en' or 'nl'), not the player's UI language. DEFAULT
-- 'en' keeps existing rows valid. A constant-default ADD COLUMN with a CHECK is
-- in-place in SQLite, so no table rebuild despite quizzes being a parent table.
ALTER TABLE quizzes ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
    CHECK (language IN ('en', 'nl'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN language;
-- +goose StatementEnd
