-- +goose Up
-- +goose StatementBegin
-- Enforce one game_questions row per (game_id, question_id) so two
-- concurrent /next calls cannot double-issue the same quiz question.
-- Without this, the read-then-insert advance path races: both callers
-- compute the same nextQuestion and both inserts succeed, producing a
-- duplicate that inflates Position and can double-bump play_count.
CREATE UNIQUE INDEX game_questions_game_question_idx ON game_questions(game_id, question_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX game_questions_game_question_idx;
-- +goose StatementEnd
