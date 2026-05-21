-- +goose Up
-- +goose StatementBegin
-- The earlier 20260511120000 backfill set username_claimed=1 only for
-- rows that had a password_hash AT THAT TIME. The seed admin (id=1)
-- usually has no password until an operator runs the -reset-password
-- CLI, which used to update password_hash only. Those rows ended up
-- with password_hash NOT NULL and username_claimed = 0, which made
-- the player client keep prompting the admin to "claim" their name
-- (#289). Re-run the same backfill now that SetPlayerPasswordHash
-- has been corrected, so existing deployments converge with no
-- operator action.
UPDATE players
SET username_claimed = 1
WHERE password_hash IS NOT NULL
  AND username_claimed = 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reverting the data backfill is not safe (we cannot know which rows
-- were 0 before the up direction ran), and the column itself stays
-- in place. Intentionally a no-op so a roll-back does not undo a
-- safe data correction.
SELECT 1;
-- +goose StatementEnd
