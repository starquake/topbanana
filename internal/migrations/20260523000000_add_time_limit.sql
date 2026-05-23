-- +goose Up
-- +goose StatementBegin
-- #99: per-quiz default time limit. NOT NULL with DEFAULT 10 so existing
-- rows inherit the prior hard-coded ten-second window (the game service's
-- defaultExpiration). CHECK clamps to a sane range — the admin form
-- enforces the same bounds; this is the DB-level backstop against a
-- bogus manual UPDATE.
ALTER TABLE quizzes ADD COLUMN time_limit_seconds INTEGER NOT NULL DEFAULT 10
    CHECK (time_limit_seconds BETWEEN 1 AND 600);
-- +goose StatementEnd

-- +goose StatementBegin
-- #99: optional per-question override. NULL means "inherit the quiz
-- default" — the game service resolves the priority chain
-- (question → quiz → defaultExpiration) at question-issue time.
ALTER TABLE questions ADD COLUMN time_limit_seconds INTEGER NULL
    CHECK (time_limit_seconds IS NULL OR time_limit_seconds BETWEEN 1 AND 600);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN time_limit_seconds;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN time_limit_seconds;
-- +goose StatementEnd
