-- +goose Up
-- password_reset_tokens carries the per-link state for the forgot-password
-- flow (#112). Mirrors email_verify_tokens but stays a separate table so
-- the two flows can diverge on TTL and consume semantics: verify links are
-- 24h and idempotent; reset links are 30min and single-use, atomic with
-- the password rotation. The token itself is never stored - only its
-- sha256 hash, so a DB leak cannot be replayed against the consume
-- endpoint. ON DELETE CASCADE keeps the table tidy when a player row is
-- removed.
-- +goose StatementBegin
CREATE TABLE password_reset_tokens
(
    token_hash  TEXT     PRIMARY KEY,
    player_id   INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL,
    consumed_at DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX password_reset_tokens_player_id_idx ON password_reset_tokens (player_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX password_reset_tokens_player_id_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE password_reset_tokens;
-- +goose StatementEnd
