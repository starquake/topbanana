-- +goose Up
-- pending_email carries the new address an authenticated visitor asked to
-- switch to (#497). NULL on every register-time row so the existing
-- consumer behaves unchanged; non-NULL on rows minted by the
-- /profile/email handler. The consume path branches on this column and
-- only swaps players.email when it is set, so a mistyped new address
-- cannot orphan the user's current verified mailbox.
-- +goose StatementBegin
ALTER TABLE email_verify_tokens ADD COLUMN pending_email TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE email_verify_tokens DROP COLUMN pending_email;
-- +goose StatementEnd
