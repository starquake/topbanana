-- +goose Up
-- Records the original uploaded filename on a media row (#1137) so the admin
-- library can show a host, as a tooltip, which file a thumbnail or audio row
-- came from - handy when matching a picture to the right question. The value is
-- the client-supplied upload filename, reduced to its base name and
-- length-capped before it is stored.
--
-- NOT NULL DEFAULT '' so every existing row gets an empty filename without a
-- backfill, and a caller that has no filename stores the empty string rather
-- than NULL. ADD COLUMN is an in-place change in SQLite (no table rebuild), so
-- even though media is a parent table (questions references it) a plain ALTER
-- inside goose's default transaction is fine, exactly as 20260620120000 added
-- description.
-- +goose StatementBegin
ALTER TABLE media ADD COLUMN original_filename TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE media DROP COLUMN original_filename;
-- +goose StatementEnd
