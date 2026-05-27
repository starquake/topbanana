-- +goose Up
-- email_verify_tokens carries the per-link state for the verify-email flow.
-- The token itself is never stored: only its sha256 hash, so a DB leak
-- cannot be replayed against the consume endpoint. ON DELETE CASCADE keeps
-- the table tidy when a player row is removed. Idempotent consume relies on
-- consumed_at IS NULL guards in the UPDATE, not a separate state column.
-- +goose StatementBegin
CREATE TABLE email_verify_tokens
(
    token_hash  TEXT     PRIMARY KEY,
    player_id   INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL,
    consumed_at DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX email_verify_tokens_player_id_idx ON email_verify_tokens (player_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX email_verify_tokens_player_id_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE email_verify_tokens;
-- +goose StatementEnd
