-- NO TRANSACTION required: SQLite ignores PRAGMA foreign_keys inside a
-- transaction, and this migration rebuilds the players table (the parent of
-- every player-keyed FK) to drop is_super_admin and rename super_admin_since.
-- PRAGMA defer_foreign_keys is not enough on a parent rebuild: a DROP TABLE on
-- the parent invalidates the child rows' references in a way the deferred check
-- at COMMIT still trips on (verified empirically against modernc.org/sqlite
-- v1.31.x and observed again here). The grandfathered foreign_keys = OFF
-- pattern from 20260506000000 / 20260528100000 applies for the same reason.
-- +goose NO TRANSACTION

-- +goose Up
-- Collapse the player / admin / super-admin model (role + is_super_admin) into
-- a single role enum 'player' | 'host' | 'admin'. The word "admin" changes
-- meaning: today's plain admin becomes Host (middle tier) and today's super
-- admin becomes Admin (top tier). A naive role rename would silently promote
-- every plain admin to the top tier, so the remap is a single atomic CASE that
-- reads is_super_admin BEFORE any row is rewritten.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE players SET role = CASE
    WHEN is_super_admin = 1 THEN 'admin'
    WHEN role = 'admin'     THEN 'host'
    ELSE 'player'
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE players_new
(
    id                INTEGER     PRIMARY KEY,
    username          TEXT UNIQUE NOT NULL,
    email             TEXT UNIQUE,
    password_hash     TEXT,
    role              TEXT        NOT NULL DEFAULT 'player',
    created_at        DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    username_claimed  INTEGER     NOT NULL DEFAULT 0,
    email_verified_at DATETIME,
    session_version   INTEGER     NOT NULL DEFAULT 0,
    role_changed_at   DATETIME,
    CHECK (password_hash IS NULL OR email IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_new (
    id, username, email, password_hash, role, created_at,
    username_claimed, email_verified_at, session_version, role_changed_at
)
SELECT id, username, email, password_hash, role, created_at,
       username_claimed, email_verified_at, session_version, super_admin_since
FROM players;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE players;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE players_new RENAME TO players;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_key_check;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE players_old
(
    id                INTEGER     PRIMARY KEY,
    username          TEXT UNIQUE NOT NULL,
    email             TEXT UNIQUE,
    password_hash     TEXT,
    role              TEXT        NOT NULL DEFAULT 'player',
    created_at        DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    username_claimed  INTEGER     NOT NULL DEFAULT 0,
    email_verified_at DATETIME,
    session_version   INTEGER     NOT NULL DEFAULT 0,
    is_super_admin    INTEGER     NOT NULL DEFAULT 0,
    super_admin_since DATETIME,
    CHECK (password_hash IS NULL OR email IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_old (
    id, username, email, password_hash, role, created_at,
    username_claimed, email_verified_at, session_version,
    is_super_admin, super_admin_since
)
SELECT id, username, email, password_hash,
       CASE WHEN role = 'host' THEN 'admin' ELSE role END,
       created_at, username_claimed, email_verified_at, session_version,
       CASE WHEN role = 'admin' THEN 1 ELSE 0 END,
       role_changed_at
FROM players;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE players;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE players_old RENAME TO players;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_key_check;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
