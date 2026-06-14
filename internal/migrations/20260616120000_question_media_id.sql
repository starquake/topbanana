-- +goose Up
-- Replace the question's legacy image_url with a media_id reference into the
-- per-quiz media library (#937, part of the #168 media epic). A question now
-- attaches an uploaded image by id instead of carrying a free-text URL; the URL
-- input was hidden pending exactly this (#426) and the player client never
-- rendered it, so dropping it is self-contained on the admin/data side.
--
-- media_id is nullable (NULL = no image). ON DELETE SET NULL implements the
-- #936 moderation rule: deleting an image clears it off any question that
-- referenced it - the question keeps its text and loses the picture rather than
-- being deleted with the image. SQLite permits ADD COLUMN with an FK only when
-- the column default is NULL, which it is, so no table rebuild is needed.
-- +goose StatementBegin
ALTER TABLE questions ADD COLUMN media_id INTEGER REFERENCES media (id) ON DELETE SET NULL;
-- +goose StatementEnd

-- image_url is a plain TEXT column (not indexed, PK, or FK), so a direct DROP
-- COLUMN works without the table-rebuild dance. Its Down counterpart in
-- 20260501000000 already drops it the same way.
-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN image_url;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions ADD COLUMN image_url TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN media_id;
-- +goose StatementEnd
