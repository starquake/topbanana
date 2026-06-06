-- +goose Up
-- +goose StatementBegin
-- MP-10 slice 3 (#687): host liveness heartbeat. The host is not a roster
-- row (the session carries host_player_id), so its presence cannot ride on
-- session_players.last_seen_at; this column tracks when the host last beat
-- its held SSE connection. Nullable with no default: NULL means the host has
-- never beat, which the runner's abandon sweep treats as "age from
-- started_at" rather than as recent activity. A constant-default-free ADD
-- COLUMN needs no table rebuild, so there is no FK-rebuild dance here.
ALTER TABLE sessions ADD COLUMN host_last_seen_at DATETIME;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN host_last_seen_at;
-- +goose StatementEnd
