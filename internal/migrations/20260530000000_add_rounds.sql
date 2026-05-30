-- NO TRANSACTION required: this migration rebuilds the questions table
-- to add a NOT NULL group_id. questions is a PARENT table (options,
-- game_questions FK-reference it), and PRAGMA defer_foreign_keys is not
-- enough on a parent rebuild: the DROP TABLE on the parent invalidates
-- the child rows' references in a way the deferred check at COMMIT still
-- trips on (verified empirically against modernc.org/sqlite, same as
-- the players rebuild in 20260529160000). The grandfathered
-- foreign_keys = OFF pattern from 20260528100000 / 20260529160000
-- applies for the same reason.
-- +goose NO TRANSACTION

-- +goose Up
-- Introduce question_groups (rounds): every question belongs to exactly
-- one group. This slice is additive - breaks stay untouched. Existing
-- breaks are NOT migrated into groups; each quiz simply gets one default
-- group ('Round 1' at position 0) holding all its questions. Later
-- slices build real round selection and the play loop on top.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- The quizzes_updated_at_on_option_* triggers (migration 20260509000000)
-- reference questions in their bodies. With legacy_alter_table OFF (the
-- default), ALTER TABLE questions_new RENAME TO questions tries to
-- rewrite those trigger bodies and fails because questions was just
-- dropped. legacy_alter_table = ON skips that reference-rewriting; the
-- trigger bodies already name questions correctly, so no rewrite is
-- needed. Must sit outside the transaction, like foreign_keys.
-- +goose StatementBegin
PRAGMA legacy_alter_table = ON;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
-- question_groups mirrors the index style of breaks_quiz_position_idx /
-- questions_quiz_position_idx: UNIQUE(quiz_id, position) so two groups
-- cannot share a slot on the same quiz. break_text is the round summary
-- authored later via the admin UI; default '' so callers can create a
-- group without supplying it. title default '' for the same reason; the
-- backfill below stamps 'Round 1' on the default group.
CREATE TABLE question_groups
(
    id         INTEGER PRIMARY KEY,
    quiz_id    INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    position   INTEGER  NOT NULL,
    title      TEXT     NOT NULL DEFAULT '',
    break_text TEXT     NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX question_groups_quiz_position_idx ON question_groups(quiz_id, position);
-- +goose StatementEnd

-- +goose StatementBegin
-- One default group per quiz, at position 0, titled 'Round 1'.
INSERT INTO question_groups (quiz_id, position, title)
SELECT id, 0, 'Round 1'
FROM quizzes;
-- +goose StatementEnd

-- +goose StatementBegin
-- Rebuild questions to add a NOT NULL group_id. Re-declare every existing
-- column and constraint: the quiz_id FK (ON DELETE CASCADE), the
-- time_limit_seconds CHECK, image_url, and the
-- questions_quiz_position_idx unique index recreated after the rename.
CREATE TABLE questions_new
(
    id                 INTEGER PRIMARY KEY,
    quiz_id            INTEGER NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    group_id           INTEGER NOT NULL REFERENCES question_groups (id) ON DELETE CASCADE,
    text               TEXT    NOT NULL DEFAULT '',
    position           INTEGER NOT NULL,
    image_url          TEXT    NOT NULL DEFAULT '',
    time_limit_seconds INTEGER CHECK (time_limit_seconds IS NULL OR (time_limit_seconds BETWEEN 1 AND 600))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Copy ids exactly and resolve each question's group_id to its quiz's
-- default (position 0) group.
INSERT INTO questions_new (id, quiz_id, group_id, text, position, image_url, time_limit_seconds)
SELECT q.id,
       q.quiz_id,
       (SELECT g.id FROM question_groups g WHERE g.quiz_id = q.quiz_id AND g.position = 0),
       q.text,
       q.position,
       q.image_url,
       q.time_limit_seconds
FROM questions q;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE questions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE questions_new RENAME TO questions;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX questions_quiz_position_idx ON questions(quiz_id, position);
-- +goose StatementEnd

-- A bare PRAGMA foreign_key_check only RETURNS the violating rows; goose
-- discards that result set, so on its own it cannot stop a broken rebuild
-- from committing. This guard turns "a FK violation exists" into a failed
-- INSERT that aborts the whole transaction (and the migration): the CHECK
-- (ok = 1) rejects the 0 produced when pragma_foreign_key_check returns
-- any row. Verified against modernc.org/sqlite.
-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA legacy_alter_table = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- Same reason as the Up: the option triggers reference questions, so the
-- RENAME below needs legacy_alter_table to skip trigger-body rewriting.
-- +goose StatementBegin
PRAGMA legacy_alter_table = ON;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
-- Rebuild questions without group_id, preserving every other column and
-- constraint. ids are copied exactly.
CREATE TABLE questions_old
(
    id                 INTEGER PRIMARY KEY,
    quiz_id            INTEGER NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    text               TEXT    NOT NULL DEFAULT '',
    position           INTEGER NOT NULL,
    image_url          TEXT    NOT NULL DEFAULT '',
    time_limit_seconds INTEGER CHECK (time_limit_seconds IS NULL OR (time_limit_seconds BETWEEN 1 AND 600))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO questions_old (id, quiz_id, text, position, image_url, time_limit_seconds)
SELECT id, quiz_id, text, position, image_url, time_limit_seconds
FROM questions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE questions;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE questions_old RENAME TO questions;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX questions_quiz_position_idx ON questions(quiz_id, position);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX question_groups_quiz_position_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE question_groups;
-- +goose StatementEnd

-- Same enforcing FK guard as the Up: abort if the rebuild left a
-- dangling reference rather than silently committing it.
-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA legacy_alter_table = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
