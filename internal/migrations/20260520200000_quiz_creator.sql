-- +goose Up
-- +goose NO TRANSACTION
-- Per the deployment decision in #169 (followed up by #281), every
-- mutating admin route must be gated on quiz ownership. This migration
-- adds the column that the creator-only-edit rule keys on AND makes
-- it NOT NULL so the rule has no "legacy bypass" loophole.
--
-- Existing rows are backfilled to the lowest-id admin player - in a
-- fresh deployment that's the seeded admin from migration
-- 20260111110308_add_admin_player.sql (id = 1). The seed migration
-- guarantees at least one admin exists by the time this migration
-- runs.
--
-- The rebuild has to disable foreign_keys for the duration:
--
--   * questions.quiz_id REFERENCES quizzes(id) ON DELETE CASCADE.
--     DROP TABLE quizzes with FKs on would cascade-delete every
--     question (and every option transitively).
--   * The 20260509000000 migration installs six AFTER INSERT/UPDATE/
--     DELETE triggers on questions and options that touch quizzes.
--     The cascade-delete above would fire them mid-rebuild, against
--     a quizzes table that no longer exists.
--
-- PRAGMA foreign_keys cannot be changed inside a transaction, so the
-- migration runs statements individually via the goose
-- NO TRANSACTION directive above. PRAGMA foreign_key_check at the
-- bottom asserts the FK graph is still consistent after the rebuild
-- before re-enabling enforcement.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- The six AFTER INSERT/UPDATE/DELETE triggers from migration
-- 20260509000000 reference "quizzes" by name. SQLite checks all
-- trigger schema integrity at RENAME time; if a trigger references a
-- name that doesn't currently exist (which is the case between
-- DROP TABLE quizzes and RENAME quizzes_new TO quizzes), the rename
-- fails. Drop the triggers first, recreate them at the bottom.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_delete;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE quizzes_new
(
    id                   INTEGER PRIMARY KEY,
    title                TEXT     NOT NULL,
    slug                 TEXT     NOT NULL UNIQUE,
    description          TEXT     NOT NULL DEFAULT '',
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by_player_id INTEGER  NOT NULL REFERENCES players (id)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO quizzes_new (id, title, slug, description, created_at, updated_at, created_by_player_id)
SELECT q.id,
       q.title,
       q.slug,
       q.description,
       q.created_at,
       q.updated_at,
       (SELECT MIN(p.id) FROM players p WHERE p.role = 'admin')
FROM quizzes q;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quizzes;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quizzes_new RENAME TO quizzes;
-- +goose StatementEnd

-- Recreate the triggers dropped above. Definitions match
-- migration 20260509000000 byte-for-byte so the trigger source
-- stays canonical.
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_insert
    AFTER INSERT ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_update
    AFTER UPDATE ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_delete
    AFTER DELETE ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_insert
    AFTER INSERT ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = NEW.question_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_update
    AFTER UPDATE ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = NEW.question_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_delete
    AFTER DELETE ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = OLD.question_id);
END;
-- +goose StatementEnd

-- A bare PRAGMA foreign_key_check only RETURNS the violating rows; goose
-- discards that result set, so on its own it cannot stop a broken rebuild from
-- committing. This guard turns "a FK violation exists" into a failed INSERT
-- that aborts the migration: the CHECK (ok = 1) rejects the 0 produced when
-- pragma_foreign_key_check returns any row.
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
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_delete;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE quizzes_old
(
    id          INTEGER PRIMARY KEY,
    title       TEXT     NOT NULL,
    slug        TEXT     NOT NULL UNIQUE,
    description TEXT     NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO quizzes_old (id, title, slug, description, created_at, updated_at)
SELECT id, title, slug, description, created_at, updated_at FROM quizzes;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quizzes;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quizzes_old RENAME TO quizzes;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_insert
    AFTER INSERT ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_update
    AFTER UPDATE ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_question_delete
    AFTER DELETE ON questions
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.quiz_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_insert
    AFTER INSERT ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = NEW.question_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_update
    AFTER UPDATE ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = NEW.question_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER quizzes_updated_at_on_option_delete
    AFTER DELETE ON options
BEGIN
    UPDATE quizzes SET updated_at = CURRENT_TIMESTAMP
    WHERE id = (SELECT quiz_id FROM questions WHERE id = OLD.question_id);
END;
-- +goose StatementEnd

-- Same enforcing FK guard as the Up: abort the migration if the rebuild
-- left any dangling reference, rather than silently re-enabling FKs.
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
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
