-- +goose Up
-- +goose StatementBegin
CREATE TABLE quizzes
(
    id          INTEGER PRIMARY KEY,
    title       TEXT        NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  INTEGER DEFAULT 0
);

CREATE TABLE questions
(
    id        INTEGER PRIMARY KEY,
    quiz_id   INTEGER NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    text      TEXT    NOT NULL DEFAULT '',
    image_url TEXT    NOT NULL DEFAULT '',
    position  INTEGER NOT NULL -- for ordering questions
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
    created_at  INTEGER DEFAULT 0
);

CREATE TABLE answers
(
    id          INTEGER PRIMARY KEY,
    player_id   INTEGER NOT NULL REFERENCES players (id),
    question_id INTEGER NOT NULL REFERENCES questions (id),
    option_id   INTEGER NOT NULL REFERENCES options (id),
    answered_at INTEGER DEFAULT 0,
    UNIQUE (player_id, question_id) -- one answer per question per player
);

CREATE TABLE quiz_attempts
(
    id           INTEGER PRIMARY KEY,
    player_id    INTEGER NOT NULL REFERENCES players (id),
    quiz_id      INTEGER NOT NULL REFERENCES quizzes (id),
    score        INTEGER NOT NULL,
    completed_at INTEGER DEFAULT 0,
    UNIQUE (player_id, quiz_id) -- one answer per question per player
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE quiz_attempts;
DROP TABLE answers;
DROP TABLE players;
DROP TABLE options;
DROP TABLE questions;
DROP TABLE quizzes;
-- +goose StatementEnd
