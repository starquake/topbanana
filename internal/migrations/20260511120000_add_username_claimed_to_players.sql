-- +goose Up
-- +goose StatementBegin
ALTER TABLE players ADD COLUMN username_claimed INTEGER NOT NULL DEFAULT 0;
-- Backfill: any row with a password_hash chose their username during
-- registration, so it counts as claimed. Anonymous rows (no password)
-- stay at the default 0, which matches the new-row case from
-- CreateAnonymousPlayer below.
UPDATE players SET username_claimed = 1 WHERE password_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE players DROP COLUMN username_claimed;
-- +goose StatementEnd
