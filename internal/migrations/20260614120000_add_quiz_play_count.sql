-- +goose Up
-- +goose StatementBegin
-- play_count is a durable hit counter on the quiz row (#891): bumped once when
-- a play of the quiz completes, never decremented. A separate counter (rather
-- than a JOIN-derived COUNT) survives the retention sweep that hard-deletes old
-- games, so the visible play total never silently shrinks. A nullable,
-- constant-default-free ADD COLUMN needs no table rebuild, so there is no
-- FK-rebuild dance here even though quizzes is a parent table.
ALTER TABLE quizzes ADD COLUMN play_count INTEGER NOT NULL DEFAULT 0
    CHECK (play_count >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill from the solo history in `games`: one bump per completed game,
-- where "completed" matches the finisher predicate used everywhere else
-- (ListPopularQuizzes, Game.IsCompleted) - every quiz question was issued.
-- The historical live-session plays cannot be reconstructed here (sessions
-- only retain their current quiz_id; earlier game_seq plays are lost), so
-- the seed is solo-only. New plays from both modes increment forward.
UPDATE quizzes
SET play_count = (
    SELECT COUNT(*)
    FROM games g
    WHERE g.quiz_id = quizzes.id
      AND (SELECT COUNT(*) FROM questions q WHERE q.quiz_id = quizzes.id) > 0
      AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
          (SELECT COUNT(*) FROM questions q WHERE q.quiz_id = quizzes.id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN play_count;
-- +goose StatementEnd
