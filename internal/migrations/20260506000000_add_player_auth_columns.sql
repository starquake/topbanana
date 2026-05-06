-- NO TRANSACTION required: SQLite ignores PRAGMA foreign_keys inside a
-- transaction, and this migration rebuilds the players table to make
-- email nullable, which needs FK enforcement disabled around the rebuild.
-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE players_new
(
    id            INTEGER     PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    email         TEXT UNIQUE,
    password_hash TEXT,
    role          TEXT        NOT NULL DEFAULT 'player',
    created_at    DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_new (id, username, email, role, created_at)
SELECT id,
       username,
       email,
       CASE WHEN id = 1 THEN 'admin' ELSE 'player' END,
       created_at
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
CREATE TABLE players_new
(
    id         INTEGER     PRIMARY KEY,
    username   TEXT UNIQUE NOT NULL,
    email      TEXT UNIQUE NOT NULL,
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO players_new (id, username, email, created_at)
SELECT id,
       username,
       COALESCE(email, username || '@local.invalid'),
       created_at
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
