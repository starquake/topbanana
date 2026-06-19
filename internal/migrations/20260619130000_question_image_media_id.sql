-- +goose Up
-- Rename questions.media_id to image_media_id for symmetry with the audio
-- reference added in 20260618120000 (#1059): a question now names its image and
-- its sound with parallel image_media_id / audio_media_id columns. SQLite's
-- RENAME COLUMN is an in-place metadata change that works on a foreign-key
-- column, so no table rebuild is needed.
-- +goose StatementBegin
ALTER TABLE questions RENAME COLUMN media_id TO image_media_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions RENAME COLUMN image_media_id TO media_id;
-- +goose StatementEnd
