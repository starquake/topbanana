-- +goose Up
-- One active room per host (#1336). Rooms that already break the rule are
-- closed first, keeping the newest one open: the same room
-- GetActiveSessionForHost returns (created_at DESC, id DESC).
-- +goose StatementBegin
UPDATE sessions
SET phase               = 'finished',
    current_question_id = NULL,
    question_started_at = NULL,
    question_expires_at = NULL,
    finished_at         = CURRENT_TIMESTAMP
WHERE phase != 'finished'
  AND EXISTS (SELECT 1
              FROM sessions newer
              WHERE newer.host_player_id = sessions.host_player_id
                AND newer.phase != 'finished'
                AND (newer.created_at > sessions.created_at
                     OR (newer.created_at = sessions.created_at AND newer.id > sessions.id)));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_sessions_one_active_per_host ON sessions (host_player_id) WHERE phase != 'finished';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_sessions_one_active_per_host;
-- +goose StatementEnd
