-- +goose Up
-- +goose StatementBegin
-- audio_repeat marks a question whose attached audio clip should be replayed up
-- to 3 times on the play surfaces (#1073). A non-nullable, constant-default
-- ADD COLUMN is an in-place metadata change; questions is not FK-referenced in a
-- way that this column touches, so no table rebuild is needed.
ALTER TABLE questions ADD COLUMN audio_repeat INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN audio_repeat;
-- +goose StatementEnd
