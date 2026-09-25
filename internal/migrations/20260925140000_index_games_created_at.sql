-- +goose Up
-- The home page lists only games from the last 30 days; without this index
-- every load scanned every game ever played (#1348).
-- +goose StatementBegin
CREATE INDEX games_created_at_idx ON games (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX games_created_at_idx;
-- +goose StatementEnd
