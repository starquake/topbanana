-- +goose Up
-- +goose StatementBegin
-- #167 slice 1: introduce a sibling-of-question break entity. Breaks
-- share the per-quiz position space with questions in slice 2 (the
-- merged-by-position play loop), but slice 1 keeps the rendering in a
-- separate admin section so the play loop and player SPA stay
-- untouched. text is the only authored content for now; image_url is
-- deferred to slice 3.
CREATE TABLE breaks
(
    id         INTEGER PRIMARY KEY,
    quiz_id    INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    position   INTEGER  NOT NULL,
    text       TEXT     NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Mirrors questions_quiz_position_idx (#352): one row per (quiz_id,
-- position) so concurrent "Add break" clicks cannot both land at the
-- same max+1 slot. NextBreakPosition + the unique index combine to
-- close the same TOCTOU race the question path closed.
CREATE UNIQUE INDEX breaks_quiz_position_idx ON breaks(quiz_id, position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX breaks_quiz_position_idx;
DROP TABLE breaks;
-- +goose StatementEnd
