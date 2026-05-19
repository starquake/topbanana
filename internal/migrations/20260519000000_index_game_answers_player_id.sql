-- +goose Up
-- The claim-name flow's leaderboard fan-out (ListQuizIDsForPlayer) filters
-- game_answers by player_id. Without an index the planner has to scan the
-- full game_answers table on every PATCH /api/players/me, which scales
-- with all answers across all quizzes. The existing UNIQUE constraint on
-- (game_id, player_id, game_question_id) cannot be reused because its
-- leftmost prefix is game_id, not player_id.
-- +goose StatementBegin
CREATE INDEX game_answers_player_id_idx ON game_answers (player_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX game_answers_player_id_idx;
-- +goose StatementEnd
