-- +goose Up
-- +goose StatementBegin
-- approved_at records when an admin cleared this account to sign in (#1227).
-- NULL means "not yet approved". A nullable ADD COLUMN is in-place in SQLite
-- (no table rebuild) even though players is a parent table. Every existing row
-- is backfilled to approved so turning LOGIN_APPROVAL_REQUIRED on later never
-- locks out an account that could already sign in.
ALTER TABLE players ADD COLUMN approved_at DATETIME;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE players SET approved_at = CURRENT_TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN approved_at;
-- +goose StatementEnd
