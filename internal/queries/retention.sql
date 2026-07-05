-- name: ListStaleAnonymousPlayerIDs :many
-- Lists ids of anonymous players minted more than 90 days ago that hold no
-- finished game. "Anonymous" is a guest who never claimed an account:
-- role 'player' with no email, no password, and a display name still
-- auto-generated (display_name_claimed = 0). created_at is the mint time
-- (there is no last-seen column), so the cutoff is "minted more than the
-- retention window ago". The window in days is a caller-supplied integer,
-- but the cutoff is computed in SQL (datetime('now', '-<days> days')) so
-- both sides of the comparison are SQLite text in the CURRENT_TIMESTAMP
-- encoding rows are minted with; a bound Go time.Time would arrive in the
-- driver's time.Time.String() text encoding and the cross-format
-- comparison would silently lie. A
-- guest with a finished game is kept regardless of age so the sweep never
-- erases a leaderboard score (#626); "finished" is every question of the
-- quiz issued as a game_question, the same finisher predicate the
-- leaderboard uses.
SELECT p.id
FROM players p
WHERE p.role = 'player'
  AND p.email IS NULL
  AND p.password_hash IS NULL
  AND p.display_name_claimed = 0
  AND p.created_at < datetime('now', '-' || CAST(sqlc.arg('days') AS INTEGER) || ' days')
  AND NOT EXISTS (
        SELECT 1
        FROM game_participants gp
                 JOIN games g ON g.id = gp.game_id
        WHERE gp.player_id = p.id
          AND (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id) > 0
          AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = g.id) >=
              (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
  );

-- name: FilterAnonymousPlayerIDs :many
-- Returns the subset of the given ids still anonymous, so the sweep spares a
-- guest claimed after the snapshot (#1175).
SELECT p.id
FROM players p
WHERE p.id IN (sqlc.slice('ids'))
  AND p.role = 'player'
  AND p.email IS NULL
  AND p.password_hash IS NULL
  AND p.display_name_claimed = 0;

-- name: ListGameIDsForPlayers :many
-- Lists every distinct game id any of the given players participates in.
-- The anonymous-player sweep snapshots these up front so the dependent
-- game_* deletes hit a stable set as the participant rows drain (#626).
SELECT DISTINCT gp.game_id
FROM game_participants gp
WHERE gp.player_id IN (sqlc.slice('player_ids'));

-- name: DeletePlayersByIDs :exec
-- Hard-deletes the given player rows. Run last in the anonymous-player sweep,
-- after every game_* row that references them has been removed. Re-asserts the
-- anonymity predicate as defense-in-depth so a since-claimed id survives (#1175).
DELETE
FROM players
WHERE id IN (sqlc.slice('ids'))
  AND role = 'player'
  AND email IS NULL
  AND password_hash IS NULL
  AND display_name_claimed = 0;

-- name: ListAbandonedGameIDs :many
-- Lists ids of games that are NOT finished and were created more than 30
-- days ago (#627). A game is "finished" when every question of its quiz
-- has been issued as a game_question -- the same finisher predicate the
-- leaderboard queries use. created_at is used (not started_at) because
-- started_at is NULL for a game that was created but never started, and
-- such a game is exactly the kind of abandonment this sweep prunes. The
-- window in days is a caller-supplied integer, but the cutoff is computed
-- in SQL (datetime('now', '-<days> days')) so both sides of the comparison
-- are SQLite text in the CURRENT_TIMESTAMP encoding rows are minted with,
-- not a cross-format Go time.Time comparison.
SELECT g.id
FROM games g
WHERE g.created_at < datetime('now', '-' || CAST(sqlc.arg('days') AS INTEGER) || ' days')
  AND NOT (
        (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id) > 0
    AND (SELECT COUNT(*) FROM game_questions gqc WHERE gqc.game_id = g.id) >=
        (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
  );

-- name: DeleteStaleAuditLog :execresult
-- Prunes admin_audit rows created more than the retention window ago (#628).
-- admin_audit is a low-volume, self-contained table (no rows FK-reference it),
-- so a single date-range DELETE suffices with no id-list batching. The window
-- in days is a caller-supplied integer, but the cutoff is computed in SQL
-- (datetime('now', '-<days> days')) so both sides of the comparison are SQLite
-- text in the CURRENT_TIMESTAMP encoding rows are minted with, not a
-- cross-format Go time.Time comparison.
DELETE
FROM admin_audit
WHERE created_at < datetime('now', '-' || CAST(sqlc.arg('days') AS INTEGER) || ' days');
