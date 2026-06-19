-- NO TRANSACTION required: SQLite ignores PRAGMA foreign_keys inside a
-- transaction, and this migration rebuilds the media table (the parent of
-- questions.media_id and questions.audio_media_id) to widen its type CHECK from
-- ('image', 'video', 'sound') to ('image', 'video', 'audio'), unifying the
-- audio kind on 'audio' (#1059). A CHECK change needs a table rebuild in SQLite.
-- PRAGMA defer_foreign_keys is not enough on a parent rebuild: DROP TABLE on the
-- parent invalidates the child rows' references in a way the deferred check at
-- COMMIT still trips on (see 20260616180000 for the same pattern).
-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN TRANSACTION;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE media_new
(
    id                   INTEGER  PRIMARY KEY AUTOINCREMENT,
    quiz_id              INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    type                 TEXT     NOT NULL DEFAULT 'image'
                                  CHECK (type IN ('image', 'video', 'audio')),
    mime                 TEXT     NOT NULL,
    path                 TEXT     NOT NULL,
    thumb_path           TEXT,
    width                INTEGER,
    height               INTEGER,
    size_bytes           INTEGER  NOT NULL,
    sha256               TEXT     NOT NULL,
    created_by_player_id INTEGER  NOT NULL REFERENCES players (id),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready                INTEGER  NOT NULL DEFAULT 1 CHECK (ready IN (0, 1)),
    duration_ms          INTEGER
);
-- +goose StatementEnd

-- Translate any legacy 'sound' row to 'audio' DURING the copy: media_new's CHECK
-- is already the new set, so a literal copy of a 'sound' row would fail the
-- CHECK. The CASE folds the data migration into the INSERT so no later UPDATE is
-- needed. Explicit ids advance sqlite_sequence's seq for media_new to MAX(id).
-- +goose StatementBegin
INSERT INTO media_new (
    id, quiz_id, type, mime, path, thumb_path, width, height,
    size_bytes, sha256, created_by_player_id, created_at, ready, duration_ms
)
SELECT id, quiz_id,
       CASE WHEN type = 'sound' THEN 'audio' ELSE type END,
       mime, path, thumb_path, width, height,
       size_bytes, sha256, created_by_player_id, created_at, ready, duration_ms
FROM media;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE media;
-- +goose StatementEnd

-- ALTER TABLE RENAME auto-renames the sqlite_sequence entry, so the cursor
-- carries across the rename without explicit handling.
-- +goose StatementBegin
ALTER TABLE media_new RENAME TO media;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX media_quiz_id_idx ON media (quiz_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX media_not_ready_idx ON media (created_at) WHERE ready = 0;
-- +goose StatementEnd

-- The fk-violation guard from 20260529160000: pragma_foreign_key_check returns
-- the violating rows, goose discards them, so on its own it cannot stop a
-- broken rebuild from committing. Convert "a violation exists" into a failing
-- CHECK so the whole transaction aborts.
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
CREATE TABLE media_old
(
    id                   INTEGER  PRIMARY KEY AUTOINCREMENT,
    quiz_id              INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    type                 TEXT     NOT NULL DEFAULT 'image'
                                  CHECK (type IN ('image', 'video', 'sound')),
    mime                 TEXT     NOT NULL,
    path                 TEXT     NOT NULL,
    thumb_path           TEXT,
    width                INTEGER,
    height               INTEGER,
    size_bytes           INTEGER  NOT NULL,
    sha256               TEXT     NOT NULL,
    created_by_player_id INTEGER  NOT NULL REFERENCES players (id),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready                INTEGER  NOT NULL DEFAULT 1 CHECK (ready IN (0, 1)),
    duration_ms          INTEGER
);
-- +goose StatementEnd

-- Reverse the kind unification: translate 'audio' back to 'sound' during the
-- copy so the legacy CHECK accepts every row.
-- +goose StatementBegin
INSERT INTO media_old (
    id, quiz_id, type, mime, path, thumb_path, width, height,
    size_bytes, sha256, created_by_player_id, created_at, ready, duration_ms
)
SELECT id, quiz_id,
       CASE WHEN type = 'audio' THEN 'sound' ELSE type END,
       mime, path, thumb_path, width, height,
       size_bytes, sha256, created_by_player_id, created_at, ready, duration_ms
FROM media;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM sqlite_sequence WHERE name = 'media';
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE media;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE media_old RENAME TO media;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX media_quiz_id_idx ON media (quiz_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX media_not_ready_idx ON media (created_at) WHERE ready = 0;
-- +goose StatementEnd

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
