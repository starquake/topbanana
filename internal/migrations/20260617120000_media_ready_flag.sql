-- +goose Up
-- Adds a two-phase-commit "ready" flag to media (#992). An upload inserts the
-- row not-ready (ready = 0), writes both files, records the paths, and only
-- then flips ready = 1. The library list filters on ready = 1, so a row whose
-- request was cancelled after the paths committed but before the flip never
-- shows in the host's library, and a sweeper later drops the stale not-ready
-- row plus its files. DEFAULT 1 so every pre-existing media row counts as
-- ready; only rows minted by the new not-ready insert start at 0.
--
-- ADD COLUMN is an in-place change in SQLite (no table rebuild), so even though
-- media is a parent table (questions.media_id references it) this needs no
-- foreign-key dance: a plain ALTER inside goose's default transaction is fine.
-- +goose StatementBegin
ALTER TABLE media ADD COLUMN ready INTEGER NOT NULL DEFAULT 1 CHECK (ready IN (0, 1));
-- +goose StatementEnd

-- Partial index over the not-ready rows so the sweeper's "stale and not ready"
-- scan stays cheap as the library grows: the common case (ready rows) is not
-- indexed, only the transient not-ready ones are.
-- +goose StatementBegin
CREATE INDEX media_not_ready_idx ON media (created_at) WHERE ready = 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX media_not_ready_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE media DROP COLUMN ready;
-- +goose StatementEnd
