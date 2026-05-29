-- +goose Up
-- is_super_admin marks a player who holds elevated powers on top of the
-- admin role: edit / delete / reset-scores on any quiz regardless of
-- creator, plus promoting and demoting other super admins. SQLite has no
-- native boolean, so the column is an INTEGER holding 0 (false) or 1
-- (true). A plain ADD COLUMN is enough here: no constraint or FK change,
-- so no table rebuild is needed.
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN is_super_admin INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- super_admin_since records when the player was promoted to super admin.
-- Nullable: NULL means the player is not a super admin (or predates this
-- column). Promote stamps it to CURRENT_TIMESTAMP; demote clears it back
-- to NULL.
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN super_admin_since DATETIME;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN super_admin_since;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE players DROP COLUMN is_super_admin;
-- +goose StatementEnd
