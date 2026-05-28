-- +goose Up

-- Pre-#446 cleanup. Once email is the login credential (#446), any
-- credentialled row with NULL email is unreachable. This migration
-- deletes those rows in dependency order so #446 + #492 can ship
-- without a legacy bypass.
--
-- Step 1 re-parents quizzes owned by a soon-to-be-deleted player to
-- the earliest credentialled-with-email player (falling back to the
-- seeded admin at id = 1 if nobody else qualifies; that row is always
-- present and password-less, so the bootstrap rule in players.sql
-- will hand quiz ownership to the next real registrant once they sign
-- up). Steps 2-3 clear participant + answer rows whose FKs to
-- players(id) have no ON DELETE CASCADE. Step 4 deletes the players;
-- player_identities, email_verify_tokens, and password_reset_tokens
-- clean up via their CASCADE FKs.
--
-- Anonymous players (password_hash IS NULL) are explicitly preserved
-- so their leaderboard + game history survives. The seeded admin
-- (id = 1, password_hash NULL, email = 'email@example.com' from
-- 20260527140000) is preserved for the same reason - its WHERE
-- check fails on password_hash.

-- +goose StatementBegin
UPDATE quizzes
SET created_by_player_id = COALESCE(
    (SELECT MIN(id) FROM players WHERE password_hash IS NOT NULL AND email IS NOT NULL),
    1
)
WHERE created_by_player_id IN (
    SELECT id FROM players
    WHERE password_hash IS NOT NULL
      AND email IS NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM game_answers
WHERE player_id IN (
    SELECT id FROM players
    WHERE password_hash IS NOT NULL
      AND email IS NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM game_participants
WHERE player_id IN (
    SELECT id FROM players
    WHERE password_hash IS NOT NULL
      AND email IS NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM players
WHERE password_hash IS NOT NULL
  AND email IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
