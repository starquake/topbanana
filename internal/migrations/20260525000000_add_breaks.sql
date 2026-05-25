-- +goose Up
-- +goose StatementBegin
-- #167: introduce a sibling-of-question break entity. The admin
-- interleaves breaks with questions on the quiz view; the play loop
-- and player SPA stay untouched until slice 2 wires them up. text is
-- the only authored content for now; image_url is deferred to slice 3.
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
-- position) so two admins cannot both place a break at the same slot.
-- breaks.position is the question position the break appears AFTER in
-- the play sequence (0 = before the first question). Breaks are anchored
-- to slots, not specific questions, so they stay put when questions are
-- reordered.
CREATE UNIQUE INDEX breaks_quiz_position_idx ON breaks(quiz_id, position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX breaks_quiz_position_idx;
DROP TABLE breaks;
-- +goose StatementEnd
