-- +goose Up
-- invites carries the per-link state for the admin-initiated invite flow
-- (#318). An admin enters an email; the row holds a single-use, time-boxed
-- token whose raw value is mailed and whose sha256 hash is stored here, so a
-- DB leak cannot be replayed against POST /accept-invite. The recipient picks
-- a username + password on the accept page; the resulting player lands already
-- email-verified because clicking the link proves control of the address.
--
-- This stays a separate per-purpose table (mirroring email_verify_tokens and
-- password_reset_tokens) rather than folding into a shared tokens table: the
-- three flows diverge on TTL, consume semantics, and the extra columns an
-- invite needs (email, status, note, accepted_at) that a reset/verify token
-- does not.
--
-- invited_by_player_id is the audit actor: who sent the invite. It is NULLABLE
-- with ON DELETE SET NULL (mirroring admin_audit.actor_player_id) so the
-- invite row survives the inviting admin's deletion with a NULL actor rather
-- than vanishing. status is the lifecycle: 'pending' on create, 'accepted'
-- when consumed, 'revoked' for the slice-2 management UI (the column is
-- declared now so the consume guard and the live-lookup can filter on it).
-- +goose StatementBegin
CREATE TABLE invites
(
    id                   INTEGER  PRIMARY KEY,
    email                TEXT     NOT NULL,
    invited_by_player_id INTEGER           REFERENCES players (id) ON DELETE SET NULL,
    token_hash           TEXT     NOT NULL UNIQUE,
    status               TEXT     NOT NULL DEFAULT 'pending'
                                  CHECK (status IN ('pending', 'accepted', 'revoked')),
    note                 TEXT,
    expires_at           DATETIME NOT NULL,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at          DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX invites_email_idx ON invites (email);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX invites_email_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE invites;
-- +goose StatementEnd
