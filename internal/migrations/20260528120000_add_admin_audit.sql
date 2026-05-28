-- +goose Up
-- admin_audit records every admin-initiated mutation against a player row
-- (verify, email_set, password_set, created, resend_verification) so a
-- question like "who flipped this account three months ago" has a real
-- answer. payload is a JSON blob of the action-specific fields; the
-- exact shape lives in the Go writer because schema-on-write keeps
-- backfills cheap. ON DELETE CASCADE on both actor + target keeps the
-- table tidy when a player row is deleted.
-- +goose StatementBegin
CREATE TABLE admin_audit
(
    id               INTEGER  PRIMARY KEY,
    actor_player_id  INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    target_player_id INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    action           TEXT     NOT NULL,
    payload          TEXT     NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX admin_audit_target_idx ON admin_audit (target_player_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX admin_audit_target_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE admin_audit;
-- +goose StatementEnd
