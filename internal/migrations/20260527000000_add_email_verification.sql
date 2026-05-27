-- +goose Up

-- players.email_verified_at flips to the verify timestamp once the
-- player consumes a verify token (#111 PR2). NULL until verified; the
-- gate added in #111 PR3 redirects unverified password-bearing rows
-- to the resend page. OAuth-linked rows are stamped at link time so
-- the gate treats them as verified (Google attests the email).
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN email_verified_at DATETIME;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN email_verified_at;
-- +goose StatementEnd
