-- +goose Up
-- +goose StatementBegin
-- #356: cover game_answers(game_question_id) so per-question lookups
-- don't fall back to a full table scan. The pre-existing
-- UNIQUE(game_id, player_id, game_question_id) has game_id leftmost
-- and can't satisfy a game_question_id-only filter.
--
-- The GetGame path no longer does the per-question lookup as of the
-- same bundle (ListAnswersByGameID groups all answers in one shot),
-- but other callers may still hit ListAnswersByGameQuestionID and
-- analytics queries are easier to reason about with this covered.
CREATE INDEX game_answers_game_question_id_idx ON game_answers(game_question_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX game_answers_game_question_id_idx;
-- +goose StatementEnd
