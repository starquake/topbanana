-- +goose Up
-- Stamp email_verified_at on the seed admin row inserted by
-- 20260111110308_add_admin_player.sql so the #111 PR3 verified-email
-- gate does not trap operators who bootstrap via `-reset-password admin`.
-- The seed email (email@example.com) is a placeholder that no resend
-- flow can deliver to; without this backfill the seeded admin can never
-- reach /admin after the gate lands. Idempotent: WHERE clause skips a
-- row that has already been verified.
-- +goose StatementBegin
UPDATE players
SET email_verified_at = CURRENT_TIMESTAMP
WHERE id = 1
  AND email_verified_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE players
SET email_verified_at = NULL
WHERE id = 1;
-- +goose StatementEnd
