-- +goose Up
-- media holds per-quiz uploaded media (#936, part of the #168 media epic). It
-- is type-discriminated (image|video|sound) so sound and video slot in later as
-- new type values with the same table and per-quiz directory; only image is
-- produced today. The bytes live on a disk volume under a per-quiz directory
-- (big media does not belong in SQLite); this row holds the relative path plus
-- metadata. sha256 is of the stored webp, computed after write, so the cleanup
-- tooling can detect a corrupt/partial file and the serving layer can reuse it
-- as the HTTP ETag. quiz_id ON DELETE CASCADE drops a quiz's library with the
-- quiz; created_by_player_id is the upload actor for the host/admin
-- permission checks the serving slice adds. The actor FK takes no ON DELETE
-- action, mirroring quizzes.created_by_player_id (20260520200000): only
-- hosts/admins upload, and those accounts are never touched by the
-- anonymous-player retention sweep, so a player delete never has to step over
-- a media row. A future hard-delete of an authoring account would be RESTRICTed
-- by this FK exactly as it already is by the quizzes one.
--
-- This is a child table (nothing references it yet), so a plain CREATE TABLE in
-- goose's default transaction is fine: no FK-rebuild dance is needed.
-- +goose StatementBegin
CREATE TABLE media
(
    id                   INTEGER  PRIMARY KEY,
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
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX media_quiz_id_idx ON media (quiz_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX media_quiz_id_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE media;
-- +goose StatementEnd
