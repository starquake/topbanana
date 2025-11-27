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

INSERT INTO quizzes (id, title, slug, description)
VALUES (1, 'Quiz 1', 'quiz-1', 'This is a quiz');
INSERT INTO quizzes (id, title, slug, description)
VALUES (2, 'Quiz 2', 'quiz-2', 'This is another quiz');

INSERT INTO questions (id, quiz_id, text, position)
VALUES (1, 1, 'What is the capital of France?', 10);
INSERT INTO questions (id, quiz_id, text, position)
VALUES (2, 1, 'What is the capital of Spain?', 20);
INSERT INTO questions (id, quiz_id, text, position)
VALUES (3, 1, 'What is the capital of Germany?', 30);
-- INSERT INTO questions (id, quiz_id, text, position) VALUES (4,1, 'What is the capital of Italy?', 4);
-- INSERT INTO questions (id, quiz_id, text, position) VALUES (5,1,'What is the capital of Poland?', 5);

INSERT INTO options (question_id, text, is_correct)
VALUES (1, 'Paris', true);
INSERT INTO options (question_id, text, is_correct)
VALUES (1, 'Berlin', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (1, 'Madrid', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (1, 'Rome', false);

INSERT INTO options (question_id, text, is_correct)
VALUES (2, 'Madrid', true);
INSERT INTO options (question_id, text, is_correct)
VALUES (2, 'Rome', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (2, 'Paris', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (2, 'Berlin', false);

INSERT INTO options (question_id, text, is_correct)
VALUES (3, 'Berlin', true);
INSERT INTO options (question_id, text, is_correct)
VALUES (3, 'Paris', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (3, 'Rome', false);
INSERT INTO options (question_id, text, is_correct)
VALUES (3, 'Madrid', false);
