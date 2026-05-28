-- +goose Up
-- admin_audit previously declared actor_player_id NOT NULL with ON DELETE
-- CASCADE, so deleting an admin destroyed every audit row where they were
-- the actor - the opposite of what an audit log is for. This rebuild makes
-- actor_player_id NULLABLE with ON DELETE SET NULL so a deleted actor
-- leaves the history intact with a NULL actor (the read path LEFT JOINs
-- players and the detail view renders "(deleted)"). target_player_id keeps
-- NOT NULL ... ON DELETE CASCADE: a target's audit trail is meaningless
-- once the target row is gone.
--
-- defer_foreign_keys postpones FK validation to COMMIT so the DROP/RENAME
-- ordering does not trip references mid-migration while still enforcing
-- the constraints at the end (per the table-rebuild convention).
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;

CREATE TABLE admin_audit_new
(
    id               INTEGER  PRIMARY KEY,
    actor_player_id  INTEGER           REFERENCES players (id) ON DELETE SET NULL,
    target_player_id INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    action           TEXT     NOT NULL,
    payload          TEXT     NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO admin_audit_new (id, actor_player_id, target_player_id, action, payload, created_at)
SELECT id, actor_player_id, target_player_id, action, payload, created_at
FROM admin_audit;

DROP TABLE admin_audit;
ALTER TABLE admin_audit_new RENAME TO admin_audit;

CREATE INDEX admin_audit_target_idx ON admin_audit (target_player_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- Reverting to NOT NULL means audit rows whose actor was deleted while
-- this migration was live cannot be represented; the INSERT below drops
-- them (WHERE actor_player_id IS NOT NULL). That is unavoidable data loss
-- on rollback - the rows have no actor to restore.
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;

CREATE TABLE admin_audit_old
(
    id               INTEGER  PRIMARY KEY,
    actor_player_id  INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    target_player_id INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    action           TEXT     NOT NULL,
    payload          TEXT     NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO admin_audit_old (id, actor_player_id, target_player_id, action, payload, created_at)
SELECT id, actor_player_id, target_player_id, action, payload, created_at
FROM admin_audit
WHERE actor_player_id IS NOT NULL;

DROP TABLE admin_audit;
ALTER TABLE admin_audit_old RENAME TO admin_audit;

CREATE INDEX admin_audit_target_idx ON admin_audit (target_player_id, created_at DESC);
-- +goose StatementEnd
