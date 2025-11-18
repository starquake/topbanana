CREATE TABLE quizzes
(
    id          INTEGER PRIMARY KEY,
    title       TEXT NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    description TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by  INTEGER NOT NULL
);

CREATE TABLE questions
(
    id        INTEGER PRIMARY KEY,
    quiz_id   INTEGER    NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    text      TEXT    NOT NULL,
    image_url TEXT,
    position  INTEGER NOT NULL -- for ordering questions
);

CREATE TABLE options
(
    id          INTEGER PRIMARY KEY,
    question_id INTEGER    NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    text        TEXT    NOT NULL,
    is_correct  BOOLEAN NOT NULL,
    position    INTEGER NOT NULL -- A, B, C, D ordering
);

CREATE TABLE players
(
    id         INTEGER PRIMARY KEY,
    username   TEXT UNIQUE NOT NULL,
    email      TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE answers
(
    id          INTEGER PRIMARY KEY,
    player_id   INTEGER NOT NULL REFERENCES players (id),
    question_id INTEGER NOT NULL REFERENCES questions (id),
    option_id   INTEGER NOT NULL REFERENCES options (id),
    answered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (player_id, question_id) -- one answer per question per player
);

CREATE TABLE quiz_attempts
(
    id           INTEGER PRIMARY KEY,
    player_id    INTEGER    NOT NULL REFERENCES players (id),
    quiz_id      INTEGER    NOT NULL REFERENCES quizzes (id),
    score        INTEGER NOT NULL,
    completed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (player_id, quiz_id) -- one answer per question per player
);

INSERT INTO quizzes (title, slug, description, created_by) VALUES ('Quiz 1', 'quiz-1', 'This is a quiz', 'admin');
INSERT INTO quizzes (title, slug, description, created_by) VALUES ('Quiz 2', 'quiz-2', 'This is another quiz', 'admin');