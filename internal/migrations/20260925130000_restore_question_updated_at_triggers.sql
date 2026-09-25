-- +goose Up
-- The questions rebuild in 20260530000000 dropped these triggers with the old
-- table and never recreated them (#1346). Definitions match 20260520200000.
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS quizzes_updated_at_on_question_insert;
-- +goose StatementEnd
