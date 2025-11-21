CREATE TABLE quizzes
(
    id          INTEGER PRIMARY KEY,
    title       TEXT        NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  DATETIME             DEFAULT CURRENT_TIMESTAMP,
    created_by  INTEGER     NOT NULL
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
    player_id    INTEGER NOT NULL REFERENCES players (id),
    quiz_id      INTEGER NOT NULL REFERENCES quizzes (id),
    score        INTEGER NOT NULL,
    completed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (player_id, quiz_id) -- one answer per question per player
);

INSERT INTO quizzes (id, title, slug, description, created_by)
VALUES (1, 'Quiz 1', 'quiz-1', 'This is a quiz', 'admin');
INSERT INTO quizzes (id, title, slug, description, created_by)
VALUES (2, 'Quiz 2', 'quiz-2', 'This is another quiz', 'admin');

INSERT INTO questions (id, quiz_id, text, position)
VALUES (1, 1, 'What is the capital of France?', 10);
INSERT INTO questions (id, quiz_id, text, position)
VALUES (2, 1, 'What is the capital of Spain?', 20);
INSERT INTO questions (id, quiz_id, text, position)
VALUES (3, 1, 'What is the capital of Germany?', 30);
-- INSERT INTO questions (id, quiz_id, text, position) VALUES (4,1, 'What is the capital of Italy?', 4);
-- INSERT INTO questions (id, quiz_id, text, position) VALUES (5,1,'What is the capital of Poland?', 5);

INSERT INTO options (question_id, text, is_correct, position)
VALUES (1, 'Paris', true, 10);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (1, 'Berlin', false, 20);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (1, 'Madrid', false, 30);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (1, 'Rome', false, 40);

INSERT INTO options (question_id, text, is_correct, position)
VALUES (2, 'Madrid', true, 40);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (2, 'Rome', false, 30);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (2, 'Paris', false, 20);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (2, 'Berlin', false, 10);

INSERT INTO options (question_id, text, is_correct, position)
VALUES (3, 'Berlin', true, 20);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (3, 'Paris', false, 10);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (3, 'Rome', false, 40);
INSERT INTO options (question_id, text, is_correct, position)
VALUES (3, 'Madrid', false, 30);
