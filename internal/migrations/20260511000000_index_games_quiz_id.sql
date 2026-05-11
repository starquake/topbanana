-- +goose Up
-- The leaderboard query (ListAnswersForQuizLeaderboard) joins game_answers
-- to games and filters by games.quiz_id. Without an index on quiz_id the
-- planner falls back to a table scan of games. Index it so the join uses
-- a SEARCH ... USING INDEX plan instead.
-- +goose StatementBegin
CREATE INDEX games_quiz_id_idx ON games (quiz_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX games_quiz_id_idx;
-- +goose StatementEnd
