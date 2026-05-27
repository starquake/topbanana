-- +goose Up
-- session_version backs the post-reset session-invalidation rule
-- (#112): the password reset handler bumps this counter and the
-- session cookie carries the value it was issued with, so every
-- in-flight cookie minted before the reset becomes invalid the moment
-- the counter changes. NOT NULL DEFAULT 0 so existing rows have a
-- well-defined starting value; pre-deploy session cookies decode with
-- an implicit version=0 (per the session module's backwards-compatible
-- parser) so they continue to validate until the first reset.
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN session_version;
-- +goose StatementEnd
