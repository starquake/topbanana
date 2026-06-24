-- +goose Up
-- +goose StatementBegin
-- SQLite creates no implicit index for a REFERENCES FK, so WHERE game_id = ?
-- on game_questions and game_participants falls back to a full table scan.
-- These queries run on every GetGame (ListGameQuestionsByGameID,
-- ListParticipantsByGameID) and grow linearly with lifetime games played.
-- game_answers is already covered by the UNIQUE(game_id, player_id,
-- game_question_id) constraint whose leftmost column is game_id.
CREATE INDEX game_questions_game_id_idx ON game_questions(game_id);
CREATE INDEX game_participants_game_id_idx ON game_participants(game_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX game_questions_game_id_idx;
DROP INDEX game_participants_game_id_idx;
-- +goose StatementEnd
