-- +goose Up
-- +goose StatementBegin
ALTER TABLE questions ADD COLUMN image_url TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE questions DROP COLUMN image_url;
-- +goose StatementEnd
