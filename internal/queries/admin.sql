-- name: InsertAdminAudit :exec
-- Records one admin-initiated mutation against a player row. payload is a
-- JSON blob the writer encodes (e.g. {"new_email":"..."} for email_set);
-- the column carries it verbatim so a future schema change need not migrate
-- existing rows. created_at falls through to the column default so every
-- row stamps in the DB's wall clock, not the caller's.
INSERT INTO admin_audit (actor_player_id, target_player_id, action, payload)
VALUES (
    sqlc.arg('actor_player_id'),
    sqlc.arg('target_player_id'),
    sqlc.arg('action'),
    sqlc.arg('payload')
);

-- name: ListAdminAuditForTarget :many
-- Returns the most-recent admin actions taken against the given target
-- player, ordered newest-first. Backs the "Recent admin actions" section
-- on the per-player detail view. The index admin_audit_target_idx covers
-- both the WHERE clause and the ORDER BY so the LIMIT is hit straight off
-- the index.
SELECT
    a.id,
    a.actor_player_id,
    CAST(COALESCE(p.display_name, '') AS TEXT) AS actor_display_name,
    a.target_player_id,
    a.action,
    a.payload,
    a.created_at
FROM admin_audit a
LEFT JOIN players p ON p.id = a.actor_player_id
WHERE a.target_player_id = sqlc.arg('target_player_id')
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg('row_limit');

-- name: CountPlayersByOnboardingState :many
-- One row per onboarding state bucket. Feeds the tab-strip counts on the
-- admin players list. The CASE expression here must match the one in
-- ListPlayersByOnboardingState below; if a branch shifts in one query but
-- not the other, the tabs will show counts that disagree with the page.
SELECT
    CASE
        WHEN EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) THEN 'oauth'
        WHEN p.email_verified_at IS NOT NULL THEN 'verified'
        WHEN p.password_hash IS NOT NULL THEN 'unverified'
        ELSE 'anonymous'
    END AS state,
    COUNT(*) AS player_count
FROM players p
GROUP BY state;

-- name: ListPlayersByOnboardingState :many
-- Page of players ordered by created_at DESC for the admin list (#450).
-- The CASE-derived onboarding_state column is matched against
-- sqlc.arg('state'); 'all' (or any unrecognised value) returns every row.
-- Branch order matters: each row must match exactly one bucket, so OAuth
-- takes precedence over verified (an OAuth-only row may not have a
-- password_hash but Google attests the email), then verified, then
-- unverified, then anonymous as the fallthrough.
SELECT
    p.*,
    EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) AS has_oauth,
    CAST(COALESCE(
        (SELECT pi.provider FROM player_identities pi WHERE pi.player_id = p.id ORDER BY pi.provider LIMIT 1),
        ''
    ) AS TEXT) AS oauth_provider,
    CAST(
        CASE
            WHEN EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) THEN 'oauth'
            WHEN p.email_verified_at IS NOT NULL THEN 'verified'
            WHEN p.password_hash IS NOT NULL THEN 'unverified'
            ELSE 'anonymous'
        END
    AS TEXT) AS onboarding_state
FROM players p
WHERE
    CAST(sqlc.arg('state') AS TEXT) = 'all'
    OR (
        CASE
            WHEN EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) THEN 'oauth'
            WHEN p.email_verified_at IS NOT NULL THEN 'verified'
            WHEN p.password_hash IS NOT NULL THEN 'unverified'
            ELSE 'anonymous'
        END
    ) = CAST(sqlc.arg('state') AS TEXT)
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('row_limit') OFFSET sqlc.arg('row_offset');

-- name: CountPlayersInOnboardingState :one
-- Total rows matching the supplied state filter; powers the page-count
-- math when a non-default ?state= filter is applied. The CASE must stay
-- in lockstep with ListPlayersByOnboardingState above.
SELECT COUNT(*) FROM players p
WHERE
    CAST(sqlc.arg('state') AS TEXT) = 'all'
    OR (
        CASE
            WHEN EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) THEN 'oauth'
            WHEN p.email_verified_at IS NOT NULL THEN 'verified'
            WHEN p.password_hash IS NOT NULL THEN 'unverified'
            ELSE 'anonymous'
        END
    ) = CAST(sqlc.arg('state') AS TEXT);

-- name: GetPlayerWithOnboardingState :one
-- Single-row variant used by the per-player detail view. Returns the
-- player columns plus the derived has_oauth / oauth_provider /
-- onboarding_state fields so the handler does not have to compute them
-- separately. The CASE must stay in lockstep with the list query above.
SELECT
    p.*,
    EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) AS has_oauth,
    CAST(COALESCE(
        (SELECT pi.provider FROM player_identities pi WHERE pi.player_id = p.id ORDER BY pi.provider LIMIT 1),
        ''
    ) AS TEXT) AS oauth_provider,
    CAST(
        CASE
            WHEN EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id) THEN 'oauth'
            WHEN p.email_verified_at IS NOT NULL THEN 'verified'
            WHEN p.password_hash IS NOT NULL THEN 'unverified'
            ELSE 'anonymous'
        END
    AS TEXT) AS onboarding_state
FROM players p
WHERE p.id = sqlc.arg('id')
LIMIT 1;

-- name: ListRecentFinishedGamesForPlayer :many
-- Newest-first finished games the given player participated in. A
-- finished game is one where every quiz question has been issued (mirrors
-- the predicate ListPlayerFinishStats uses). Joined with quizzes so the
-- detail view can render the quiz title without a second round trip.
-- LIMIT is supplied by the caller so this query stays reusable for
-- future "last N games" surfaces.
SELECT
    g.id        AS game_id,
    g.quiz_id   AS quiz_id,
    CAST(q.title AS TEXT) AS quiz_title,
    g.created_at AS created_at
FROM game_participants gp
JOIN games g ON g.id = gp.game_id
JOIN quizzes q ON q.id = g.quiz_id
WHERE gp.player_id = sqlc.arg('player_id')
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = g.quiz_id)
  AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
ORDER BY g.created_at DESC, g.id DESC
LIMIT sqlc.arg('row_limit');

-- name: SetPlayerEmailVerifiedNow :execrows
-- Stamps email_verified_at to the current time even when the column is
-- already populated. Used by the admin "Mark verified" action where the
-- intent is to flip an unverified row; the action also rewrites an
-- already-verified row to refresh the timestamp. Returns the number of
-- affected rows so the wrapper can map "no rows" to ErrPlayerNotFound.
UPDATE players
SET email_verified_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg('id');

-- name: SetPlayerEmail :execrows
-- Updates players.email and clears email_verified_at so a changed address
-- must be re-proven. Used by the admin "Set / overwrite email" action;
-- the admin then marks the account verified (or triggers a resend) once
-- the new address should be treated as proven. Clearing verification keeps
-- the onboarding bucket honest: a freshly-set address starts unverified
-- rather than inheriting the old address's verified state. A UNIQUE
-- collision on players.email surfaces as the driver's constraint error
-- which the store wrapper maps to auth.ErrEmailTaken so the handler can
-- render a clean banner.
UPDATE players
SET email = sqlc.arg('email'),
    email_verified_at = NULL
WHERE id = sqlc.arg('id');

-- name: CreatePlayerByAdmin :one
-- Admin-initiated player creation (#450). Stamps email_verified_at at
-- CURRENT_TIMESTAMP so the new row bypasses the email loop entirely; the
-- admin hands the credentials to the recipient out-of-band. password_hash
-- is nullable on the column but the handler enforces a non-empty hash so
-- the row can log in immediately. role is fixed to 'player' - the admin
-- promotion CASE only fires on the public register/oauth paths.
INSERT INTO players (display_name, email, password_hash, email_verified_at, role, display_name_claimed)
VALUES (
    sqlc.arg('display_name'),
    sqlc.arg('email'),
    sqlc.arg('password_hash'),
    CURRENT_TIMESTAMP,
    'player',
    1
)
RETURNING *;
