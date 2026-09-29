-- +goose Up
-- SQLite creates no implicit index for a REFERENCES column, so every FK check
-- and ON DELETE action on the parent scanned the child table (#1345). Columns
-- already leading a composite or UNIQUE index are left out.
-- +goose StatementBegin
CREATE INDEX admin_audit_actor_player_id_idx ON admin_audit (actor_player_id);
CREATE INDEX game_answers_option_id_idx ON game_answers (option_id);
CREATE INDEX game_participants_quiz_id_idx ON game_participants (quiz_id);
CREATE INDEX game_questions_question_id_idx ON game_questions (question_id);
CREATE INDEX game_seen_rounds_round_id_idx ON game_seen_rounds (round_id);
CREATE INDEX invites_invited_by_player_id_idx ON invites (invited_by_player_id);
CREATE INDEX media_created_by_player_id_idx ON media (created_by_player_id);
CREATE INDEX options_question_id_idx ON options (question_id);
CREATE INDEX questions_audio_media_id_idx ON questions (audio_media_id);
CREATE INDEX questions_image_media_id_idx ON questions (image_media_id);
CREATE INDEX questions_round_id_idx ON questions (round_id);
CREATE INDEX quizzes_created_by_player_id_idx ON quizzes (created_by_player_id);
CREATE INDEX session_answers_option_id_idx ON session_answers (option_id);
CREATE INDEX session_answers_player_id_idx ON session_answers (player_id);
CREATE INDEX session_answers_question_id_idx ON session_answers (question_id);
CREATE INDEX session_players_player_id_idx ON session_players (player_id);
CREATE INDEX sessions_current_question_id_idx ON sessions (current_question_id);
CREATE INDEX sessions_current_round_id_idx ON sessions (current_round_id);
CREATE INDEX sessions_host_player_id_idx ON sessions (host_player_id);
CREATE INDEX sessions_quiz_id_idx ON sessions (quiz_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX sessions_quiz_id_idx;
DROP INDEX sessions_host_player_id_idx;
DROP INDEX sessions_current_round_id_idx;
DROP INDEX sessions_current_question_id_idx;
DROP INDEX session_players_player_id_idx;
DROP INDEX session_answers_question_id_idx;
DROP INDEX session_answers_player_id_idx;
DROP INDEX session_answers_option_id_idx;
DROP INDEX quizzes_created_by_player_id_idx;
DROP INDEX questions_round_id_idx;
DROP INDEX questions_image_media_id_idx;
DROP INDEX questions_audio_media_id_idx;
DROP INDEX options_question_id_idx;
DROP INDEX media_created_by_player_id_idx;
DROP INDEX invites_invited_by_player_id_idx;
DROP INDEX game_seen_rounds_round_id_idx;
DROP INDEX game_questions_question_id_idx;
DROP INDEX game_participants_quiz_id_idx;
DROP INDEX game_answers_option_id_idx;
DROP INDEX admin_audit_actor_player_id_idx;
-- +goose StatementEnd
