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
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE games
(
    id         VARCHAR(20) PRIMARY KEY,
    quiz_id    INTEGER  NOT NULL REFERENCES quizzes (id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME

);

CREATE TABLE game_participants
(
    id        INTEGER PRIMARY KEY,
    game_id   VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id INTEGER     NOT NULL REFERENCES players (id),
    joined_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE game_questions
(
    id          INTEGER PRIMARY KEY,
    game_id     VARCHAR(20) NOT NULL REFERENCES games (id),
    question_id INTEGER     NOT NULL REFERENCES questions (id),
    started_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expired_at  DATETIME
);

CREATE TABLE game_answers
(
    id               INTEGER PRIMARY KEY,
    game_id          VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id        INTEGER     NOT NULL REFERENCES players (id),
    game_question_id INTEGER     NOT NULL REFERENCES game_questions (id),
    option_id        INTEGER     NOT NULL REFERENCES options (id),
    answered_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (game_id, player_id, game_question_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE game_answers;
DROP TABLE game_questions;
DROP TABLE game_participants;
DROP TABLE games;
DROP TABLE players;
DROP TABLE options;
DROP TABLE questions;
DROP TABLE quizzes;
-- +goose StatementEnd
