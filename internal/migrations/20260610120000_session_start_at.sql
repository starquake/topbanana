-- +goose Up
-- +goose StatementBegin
-- #735 host-armed last-call countdown. When the host arms the start, this
-- column holds the absolute server deadline at which the runner begins the
-- game; NULL means no countdown is armed. The runner reads it on each lobby
-- tick and starts the game once now >= start_at; the /state read surfaces it
-- so every surface (host TV + player lobbies) renders the same server-clock
-- countdown. Nullable with no default; a constant-default-free ADD COLUMN
-- needs no table rebuild, so there is no FK-rebuild dance here.
ALTER TABLE sessions ADD COLUMN start_at DATETIME;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN start_at;
-- +goose StatementEnd
