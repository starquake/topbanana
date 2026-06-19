-- +goose Up
-- Adds the storage foundation for question sounds (#1059), mirroring the image
-- media feature. A media row can now record an audio clip's playback length,
-- and a question can reference an uploaded sound separately from its image.
--
-- duration_ms is nullable (NULL = unknown length): audio is not decoded
-- server-side, so the value is supplied by the caller (read in-browser) and may
-- be absent. ADD COLUMN is an in-place change in SQLite (no table rebuild), so
-- even though media is a parent table (questions references it) this needs no
-- foreign-key dance: a plain ALTER inside goose's default transaction is fine,
-- exactly as 20260617120000 added the ready column.
-- +goose StatementBegin
ALTER TABLE media ADD COLUMN duration_ms INTEGER;
-- +goose StatementEnd

-- audio_media_id is a second media reference on questions, kept separate from
-- media_id so a question can carry both an image and a sound. It is nullable
-- (NULL = no sound). ON DELETE SET NULL clears it when the sound is deleted, so
-- moderating a sound leaves the question intact minus its audio - the same rule
-- 20260616120000 gave media_id. SQLite permits ADD COLUMN with an FK only when
-- the column default is NULL, which it is, so no table rebuild is needed.
-- +goose StatementBegin
ALTER TABLE questions ADD COLUMN audio_media_id INTEGER REFERENCES media (id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN audio_media_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE media DROP COLUMN duration_ms;
-- +goose StatementEnd
