-- +goose Up
-- One active room per host (#1336). Rooms that already break the rule are
-- closed first. The survivor is the room with a game in flight, then the one
-- the host beat on most recently, then the newest, so a deploy never ends a
-- running game in favour of an empty duplicate lobby.
-- +goose StatementBegin
UPDATE sessions
SET phase               = 'finished',
    current_question_id = NULL,
    question_started_at = NULL,
    question_expires_at = NULL,
    finished_at         = CURRENT_TIMESTAMP
WHERE id IN (SELECT id
             FROM (SELECT id,
                          ROW_NUMBER() OVER (
                              PARTITION BY host_player_id
                              ORDER BY phase IN ('round_intro', 'question', 'reveal', 'round_results') DESC,
                                  host_last_seen_at IS NULL,
                                  host_last_seen_at DESC,
                                  created_at DESC,
                                  id DESC
                              ) AS keep_rank
                   FROM sessions
                   WHERE phase != 'finished') ranked
             WHERE keep_rank > 1);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_sessions_one_active_per_host ON sessions (host_player_id) WHERE phase != 'finished';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_sessions_one_active_per_host;
-- +goose StatementEnd
