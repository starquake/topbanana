-- +goose Up
-- +goose StatementBegin
-- #352: enforce one question per (quiz_id, position) so concurrent
-- "Add question" clicks on the same quiz can't both land at the same
-- max+1 slot. Pre-index, NextQuestionPosition + CreateQuestion ran as
-- two separate non-transactional calls, leaving a TOCTOU race that
-- produced two questions at the same position; ORDER BY position then
-- had an undefined tie-break order and SwapQuestionPositions saw them
-- as the same neighbour.
--
-- Pre-existing data may already contain duplicate positions (the race
-- went unguarded for the entire history of the project, plus older
-- bulk-create paths defaulted Position to 0). Renumber within each
-- quiz before adding the index so the migration applies cleanly on
-- any DB. The new order preserves the existing position ordering,
-- breaks ties by id (the insert order), and produces a dense 1..N
-- sequence which the admin's reorder UI already assumes.
UPDATE questions
SET position = (
    SELECT rownum
    FROM (
        SELECT id AS rid,
               ROW_NUMBER() OVER (PARTITION BY quiz_id ORDER BY position, id) AS rownum
        FROM questions
    ) AS r
    WHERE r.rid = questions.id
);

CREATE UNIQUE INDEX questions_quiz_position_idx ON questions(quiz_id, position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX questions_quiz_position_idx;
-- The renumber above is one-way; restoring the original
-- duplicate-position state would need a snapshot we don't take. The
-- down direction therefore only drops the index.
-- +goose StatementEnd
