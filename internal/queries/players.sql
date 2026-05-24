-- name: GetPlayerByUsername :one
SELECT *
FROM players
WHERE username = ?
LIMIT 1;

-- name: CreatePlayerWithCredentials :one
-- The role decision lives in SQL so the "first credentialled registrant
-- becomes admin" rule is atomic. Two concurrent first-registrations would
-- both observe count == 0 if we computed the role in Go and called INSERT
-- separately, leaving us with two admins. Folding the check into the same
-- INSERT serialises the decision against the row that gets written.
--
-- The third placeholder is the role requested by the caller (env-list match,
-- otherwise "player"). If "admin" is requested explicitly we honour that;
-- otherwise we promote only when no credentialled player exists yet. A
-- "credentialled" player has either a password_hash or a linked OAuth
-- identity (player_identities row). The seeded admin (id=1) has neither
-- and is intentionally ignored so the operator's first real registration
-- replaces it as admin.
--
-- username_claimed is set to 1 because a registering user explicitly chose
-- their username at the register form. The column tracks "did the player
-- pick this name themselves" (vs auto-generated petname), so a fresh
-- registrant must be marked as claimed from the moment the row is written.
INSERT INTO players (username, password_hash, role, username_claimed)
VALUES (
    sqlc.arg('username'),
    sqlc.arg('password_hash'),
    CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN NOT EXISTS (
            SELECT 1 FROM players p
            WHERE p.password_hash IS NOT NULL
               OR EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id)
        ) THEN 'admin'
        ELSE 'player'
    END,
    1
)
RETURNING *;

-- name: CreateAnonymousPlayer :one
-- Used by the EnsurePlayer middleware to back a fresh visitor with a real
-- players row before they can play. email and password_hash are NULL; role
-- is fixed to 'player' because the "first password-bearing registrant
-- becomes admin" SQL above filters by password_hash IS NOT NULL, so an
-- anonymous row never qualifies for promotion.
--
-- username_claimed defaults to 0: the auto-generated petname is not a name
-- the visitor picked, so the row is unclaimed until they rename via the
-- PATCH /api/players/me endpoint or sign up through ClaimPlayer below.
INSERT INTO players (username, role)
VALUES (sqlc.arg('username'), 'player')
RETURNING *;

-- name: ClaimPlayer :one
-- Upgrades an anonymous (password_hash IS NULL) row in place so that an
-- already-playing visitor keeps their player_id when they sign up. The
-- WHERE password_hash IS NULL guard makes this idempotent: a second claim
-- attempt against an already-credentialled row returns no rows, which the
-- store maps to ErrPlayerAlreadyClaimed.
--
-- The role CASE mirrors CreatePlayerWithCredentials so the "first
-- credentialled registrant becomes admin" rule still triggers when the
-- very first sign-up happens through the claim path (i.e. the registrant
-- played anonymously first). The subquery aliases the players table as pp
-- so the column reference in the WHERE is unambiguous against the row
-- being updated. The credentialled-player check covers both password and
-- OAuth identity so a deployment that bootstrapped its admin via Google
-- doesn't auto-promote later password claimers.
--
-- username_claimed is set to 1 because the visitor is explicitly choosing
-- their username via the register form. This is the register-after-playing
-- path: the row now represents a player who picked their own name, so it
-- must look identical to a CreatePlayerWithCredentials row to downstream
-- callers.
UPDATE players
SET username = sqlc.arg('username'),
    password_hash = sqlc.arg('password_hash'),
    role = CASE
        WHEN CAST(sqlc.arg('requested_role') AS TEXT) = 'admin' THEN 'admin'
        WHEN NOT EXISTS (
            SELECT 1 FROM players p
            WHERE p.password_hash IS NOT NULL
               OR EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id)
        ) THEN 'admin'
        ELSE 'player'
    END,
    username_claimed = 1
WHERE players.id = sqlc.arg('id')
  AND players.password_hash IS NULL
RETURNING *;

-- name: SetPlayerPasswordHash :execrows
-- Used by the cmd/server -reset-password operator tool to rotate a single
-- player's password without disturbing username / role / email. Returns the
-- number of affected rows so the caller can map "no rows" to a "username not
-- found" error.
--
-- username_claimed is set to 1 alongside the password because once an
-- operator has set a password on a row, the username is no longer an
-- auto-assigned petname the player should be nudged to replace (#289). The
-- migration 20260511120000 ran the same backfill at the time, but only for
-- rows that already had a password_hash; later password sets via this
-- query previously left username_claimed at 0, which made the seed
-- admin (id=1) keep popping the claim-name modal in the player client.
UPDATE players
SET password_hash    = sqlc.arg('password_hash'),
    username_claimed = 1
WHERE username = sqlc.arg('username');

-- name: GetPlayerByEmail :one
-- Look up a player by email so the Google OAuth callback can link a
-- fresh identity onto an existing row when the verified email matches
-- (instead of creating a duplicate player). Returns sql.ErrNoRows when
-- no row matches, which the store maps to ErrPlayerNotFound.
SELECT *
FROM players
WHERE email = ?
LIMIT 1;

-- name: GetPlayerByProviderSubject :one
-- Look up a player via their player_identities row. The OAuth callback
-- runs this first; on a hit the caller knows the identity already
-- exists and signs the player in without touching the email-based
-- linking path.
SELECT p.*
FROM players p
JOIN player_identities pi ON pi.player_id = p.id
WHERE pi.provider = ? AND pi.subject = ?
LIMIT 1;

-- name: CreatePlayerFromOAuth :one
-- Insert a brand-new player row for a first-time OAuth sign-in. No
-- password_hash (the player has no local credential), email comes from
-- the verified id-token claim, username_claimed is set to 1 because the
-- caller supplies an auto-generated petname that the player will be
-- prompted to change via the existing claim-name modal.
--
-- The role CASE mirrors CreatePlayerWithCredentials so an OAuth-only
-- first registrant still earns the admin promotion atomically. This is
-- intentional: a deployment that only uses Google sign-in must still
-- be able to bootstrap its first admin without an out-of-band password
-- step. Counting credentialled players (password OR linked OAuth
-- identity) instead of only password-bearing rows keeps OAuth-only
-- deployments from promoting *every* sign-in to admin. Without this,
-- the second-and-onward Google sign-ins on a fresh DB would all see
-- count(password_hash IS NOT NULL) == 0 and become admin.
INSERT INTO players (username, email, role, username_claimed)
VALUES (
    sqlc.arg('username'),
    sqlc.arg('email'),
    CASE
        WHEN NOT EXISTS (
            SELECT 1 FROM players p
            WHERE p.password_hash IS NOT NULL
               OR EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id)
        ) THEN 'admin'
        ELSE 'player'
    END,
    1
)
RETURNING *;

-- name: LinkProviderIdentity :exec
-- Attach a (provider, subject) pair to an existing players row. Called
-- after CreatePlayerFromOAuth for the new-account path and after
-- GetPlayerByEmail for the existing-email link path. The UNIQUE
-- constraint on (provider, subject) prevents two players from claiming
-- the same external identity.
INSERT INTO player_identities (player_id, provider, subject)
VALUES (?, ?, ?);

-- name: ClaimPlayerForOAuth :one
-- Upgrades a fully anonymous players row (no password_hash, no email)
-- in place by attaching the OAuth-verified email. Lets a visitor who
-- played anonymously keep their existing player_id (and therefore
-- their game history and any custom username) when they sign in with
-- Google for the first time. The username is left untouched: the
-- visitor's auto-petname or PATCH-claimed name carries through onto
-- the OAuth-linked row.
--
-- The role CASE mirrors CreatePlayerFromOAuth so the first OAuth-only
-- registrant still earns the admin promotion atomically. ELSE 'player'
-- matches CreateAnonymousPlayer's fixed default; anonymous rows
-- always start as 'player', so re-asserting it is a no-op in
-- practice.
--
-- The WHERE guards (password_hash IS NULL AND email IS NULL) make
-- the update idempotent under concurrent callbacks. A second
-- callback that lost the race sees the row already credentialled
-- and matches no rows; the wrapper maps that to ErrPlayerNotFound
-- so the handler can fall through to the create path with the same
-- petname-collision retry it uses for cookieless visitors.
UPDATE players
SET email = sqlc.arg('email'),
    role = CASE
        WHEN NOT EXISTS (
            SELECT 1 FROM players p
            WHERE p.password_hash IS NOT NULL
               OR EXISTS (SELECT 1 FROM player_identities pi WHERE pi.player_id = p.id)
        ) THEN 'admin'
        ELSE 'player'
    END
WHERE players.id = sqlc.arg('id')
  AND players.password_hash IS NULL
  AND players.email IS NULL
RETURNING *;

-- name: UpdatePlayerUsername :one
-- Updates the username on an anonymous player row in place. The WHERE
-- clause refuses the update when the player has already claimed a
-- non-anonymous identity (password_hash IS NOT NULL), so the SQL is the
-- atomic guard against a stale anonymous check in the service layer.
-- Returns the updated row when one was affected; the wrapper distinguishes
-- "not anonymous anymore" (sql.ErrNoRows) from "username collision"
-- (UNIQUE constraint failure on players.username).
--
-- username_claimed is set to 1 because this is the dedicated claim-name
-- endpoint (PATCH /api/players/me); the visitor is explicitly picking
-- their display name. After this update the row reads as "player chose
-- this name" identically to the credentialled-registration path.
UPDATE players
SET username = sqlc.arg('username'),
    username_claimed = 1
WHERE id = sqlc.arg('id') AND password_hash IS NULL
RETURNING *;

-- name: RenamePlayer :one
-- Renames any player row by id, regardless of password / email / role.
-- The dedicated profile-page endpoint (POST /profile/username, #410)
-- uses this so authenticated players (password, OAuth, admin) can
-- change their display name. Anonymous rows have their own narrower
-- path via UpdatePlayerUsername above; this query is intentionally
-- not gated by password_hash so the OAuth-only and admin cases also
-- work.
--
-- Returns the updated row when one was affected; the store wrapper
-- maps sql.ErrNoRows to ErrPlayerNotFound and a UNIQUE constraint
-- failure on players.username to ErrUsernameTaken so the handler can
-- map both onto user-facing form errors.
UPDATE players
SET username = sqlc.arg('username'),
    username_claimed = 1
WHERE id = sqlc.arg('id')
RETURNING *;
