-- +goose Up
-- SQLite refuses to add a column with a non-constant DEFAULT (CURRENT_TIMESTAMP)
-- via ALTER TABLE, so we add it with a literal placeholder, backfill from
-- created_at, and use an INSERT trigger to set new rows to CURRENT_TIMESTAMP.
-- +goose StatementBegin
ALTER TABLE quizzes
    ADD COLUMN updated_at TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00';
-- +goose StatementEnd

-- Backfill existing rows: created_at is the only signal we have, use it as
-- the initial updated_at so the list ordering is meaningful from day one.
-- +goose StatementBegin
UPDATE quizzes SET updated_at = created_at;
-- +goose StatementEnd

-- New rows: the CreateQuiz query sets updated_at = CURRENT_TIMESTAMP
-- explicitly. The literal default above is only a safety net for any future
-- INSERT that bypasses the named query.

-- Triggers: any write to questions or options bumps the parent quiz so the
-- list ordering reflects the whole quiz tree, not just direct quiz edits.
-- Direct quiz updates are handled by the UpdateQuiz query setting
-- updated_at = CURRENT_TIMESTAMP explicitly.
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

-- For options we have to look up the quiz via the parent question.
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_option_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_insert;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN updated_at;
-- +goose StatementEnd
