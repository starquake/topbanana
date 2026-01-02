-- +goose Up
-- +goose StatementBegin
CREATE TABLE quizzes
(
    id          INTEGER PRIMARY KEY,
    title       TEXT        NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE questions
(
    id       INTEGER PRIMARY KEY,
    quiz_id  INTEGER NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    text     TEXT    NOT NULL DEFAULT '',
    position INTEGER NOT NULL -- for ordering questions
);

CREATE TABLE options
(
    id          INTEGER PRIMARY KEY,
    question_id INTEGER NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    text        TEXT    NOT NULL,
    is_correct  BOOLEAN NOT NULL
);

CREATE TABLE players
(
    id         INTEGER PRIMARY KEY,
    username   TEXT UNIQUE NOT NULL,
    email      TEXT UNIQUE NOT NULL,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE players;
DROP TABLE options;
DROP TABLE questions;
DROP TABLE quizzes;
-- +goose StatementEnd
