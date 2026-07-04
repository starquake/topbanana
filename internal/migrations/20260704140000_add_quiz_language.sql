-- +goose Up
-- +goose StatementBegin
-- language is an advisory content label (#1115): the language the quiz's
-- questions are written in ('en' or 'nl'). It does not change the player's UI
-- language (that is browser/cookie driven) and does not filter any list. NOT
-- NULL with DEFAULT 'en' so existing rows stay valid; the CHECK enforces the
-- same set the admin form's selector exposes. A constant-default ADD COLUMN
-- with a CHECK is in-place in SQLite, so no table rebuild despite quizzes
-- being a parent table.
ALTER TABLE quizzes ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
    CHECK (language IN ('en', 'nl'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN language;
-- +goose StatementEnd
