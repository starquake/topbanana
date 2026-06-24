-- NO TRANSACTION required: SQLite ignores PRAGMA foreign_keys inside a
-- transaction, and this migration rebuilds the players table (the
-- parent of every player-keyed FK) which requires FK enforcement to
-- be disabled around the rebuild. PRAGMA defer_foreign_keys is not
-- enough on a parent rebuild: a DROP TABLE on the parent invalidates
-- the child rows' references in a way the deferred check at COMMIT
-- still trips on (verified empirically against modernc.org/sqlite
-- v1.31.x). The grandfathered pattern from 20260506000000 applies
-- here for the same reason.
-- +goose NO TRANSACTION

-- +goose Up
-- Tighten the players table so a credentialled row (password_hash IS
-- NOT NULL) must also carry an email. Anonymous rows (no password_hash)
-- can still have a NULL email because they never log in by credential.
--
-- Enforced via a CHECK constraint rather than a partial UNIQUE index
-- because the semantics we want are "credentialled implies email", not
-- "email is unique among credentialled rows" - the existing
-- email TEXT UNIQUE already handles the uniqueness side.
--
-- SQLite cannot add a CHECK constraint with ALTER TABLE, so this is a
-- table rebuild. Pre-#446 the staging DB has been purged of pre-email
-- credentialled rows by migration 20260528000000, so no backfill step
-- is needed here.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
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
    CHECK (password_hash IS NULL OR email IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_new (
    id, username, email, password_hash, role, created_at,
    username_claimed, email_verified_at, session_version
)
SELECT id, username, email, password_hash, role, created_at,
       username_claimed, email_verified_at, session_version
FROM players;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE players;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE players_new RENAME TO players;
-- +goose StatementEnd

-- A bare PRAGMA foreign_key_check only RETURNS the violating rows; goose
-- discards that result set, so on its own it cannot stop a broken rebuild from
-- committing. This guard turns "a FK violation exists" into a failed INSERT
-- that aborts the whole transaction (and the migration): the CHECK (ok = 1)
-- rejects the 0 produced when pragma_foreign_key_check returns any row.
-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
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
    session_version   INTEGER     NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_old (
    id, username, email, password_hash, role, created_at,
    username_claimed, email_verified_at, session_version
)
SELECT id, username, email, password_hash, role, created_at,
       username_claimed, email_verified_at, session_version
FROM players;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE players;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE players_old RENAME TO players;
-- +goose StatementEnd

-- Same enforcing FK guard as the Up: abort the transaction if the rebuild
-- left any dangling reference, rather than silently committing it.
-- +goose StatementBegin
CREATE TEMP TABLE _fk_guard (ok INTEGER CHECK (ok = 1));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _fk_guard (ok)
SELECT CASE WHEN (SELECT count(*) FROM pragma_foreign_key_check) = 0 THEN 1 ELSE 0 END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE _fk_guard;
-- +goose StatementEnd

-- +goose StatementBegin
COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
