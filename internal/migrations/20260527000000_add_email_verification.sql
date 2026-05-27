-- +goose Up
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN email_verified_at DATETIME;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN email_verified_at;
-- +goose StatementEnd
