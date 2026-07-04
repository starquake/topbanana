-- name: ListPopularQuizzes :many
-- Returns the most-played quizzes over the last 30 days, scored by number of
-- finished games. A game counts as finished when every quiz question has
-- been issued, i.e. the count of game_questions rows for the game has
-- caught up with the count of questions on the quiz. Same finisher
-- condition as ListAnswersForQuizLeaderboard so the home page and the
-- per-quiz leaderboard agree on what "played" means. Host preview games
-- (is_preview = 1, #1192) are excluded so a draft an owner only previewed
-- never surfaces here and a preview never inflates the ranking.
--
-- The EXISTS gate on questions excludes quizzes with zero questions:
-- without it the finisher predicate above degenerates to 0 >= 0 and
-- promotes empty quizzes to the popular list (#275). It also keeps
-- the home query consistent with game.Game.IsCompleted, which only
-- treats a game as finished when the quiz has at least one question.
--
-- No LIMIT in SQL: sqlc's SQLite parser truncates the surrounding
-- statement in multi-query files when a LIMIT clause is present, so the
-- caller slices the result. Real-world traffic is tiny enough that this
-- is fine.
--
-- Visibility gate (#103): only quizzes the public can play surface on
-- the start page. Unlisted is link-only; private requires a logged-in
-- player, neither of which fits this anonymous list. Mode gate (MP-0 /
-- #677): live quizzes are hosted-only and never solo-playable, so they
-- are excluded from the start page too. Published gate (#1192): a draft
-- is not yet playable, so it must not surface here either.
--
-- Two play tallies, deliberately distinct: recent_play_count is the
-- 30-day finished-game count that drives the ranking (what "popular"
-- means here), while play_count is the durable lifetime counter on the
-- quiz row (#891) the card displays, matching the admin/host cards. The
-- displayed figure and the ranking key are separate so a quiz with many
-- recent plays but an out-of-sync durable counter still ranks by recency
-- yet shows its true lifetime total.
SELECT q.id          AS id,
       q.title       AS title,
       q.slug        AS slug,
       q.description AS description,
       q.created_at  AS created_at,
       q.play_count  AS play_count,
       p.display_name AS created_by_display_name,
       COUNT(DISTINCT g.id) AS recent_play_count,
       (SELECT COUNT(*) FROM rounds rc WHERE rc.quiz_id = q.id) AS round_count,
       (SELECT COUNT(*) FROM questions qc2 WHERE qc2.quiz_id = q.id) AS question_count
FROM quizzes q
JOIN players p ON p.id = q.created_by_player_id
JOIN games g ON g.quiz_id = q.id
WHERE g.created_at >= datetime('now', '-30 days')
  AND q.visibility = 'public'
  AND q.mode = 'solo'
  AND q.published = 1
  AND g.is_preview = 0
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = q.id)
  AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = q.id)
GROUP BY q.id
ORDER BY recent_play_count DESC, q.updated_at DESC;

-- name: ListNewestQuizzes :many
-- Returns the most-recently-created public quizzes, newest first. Backs
-- the "Newest" tab on the start page so freshly-authored quizzes are
-- discoverable before they accumulate enough plays to reach the popular
-- list.
--
-- Visibility gate (#103): only quizzes the public can play surface on
-- the start page. Unlisted is link-only; private requires a logged-in
-- player, neither of which fits this anonymous list. Mode gate (MP-0 /
-- #677): live quizzes are hosted-only, so they are excluded here too.
-- Published gate (#1192): a draft is not yet playable, so it is excluded.
--
-- The EXISTS gate on questions excludes quizzes with zero questions:
-- they cannot be played, so they have no business on a "pick a quiz"
-- list (#275). Same exclusion ListPopularQuizzes applies.
--
-- No LIMIT in SQL: sqlc's SQLite parser truncates the surrounding
-- statement in multi-query files when a LIMIT clause is present, so the
-- caller slices the result. See ListPopularQuizzes above.
SELECT q.id          AS id,
       q.title       AS title,
       q.slug        AS slug,
       q.description AS description,
       q.created_at  AS created_at,
       q.play_count  AS play_count,
       p.display_name AS created_by_display_name,
       (SELECT COUNT(*) FROM rounds rc WHERE rc.quiz_id = q.id) AS round_count,
       (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = q.id) AS question_count
FROM quizzes q
JOIN players p ON p.id = q.created_by_player_id
WHERE q.visibility = 'public'
  AND q.mode = 'solo'
  AND q.published = 1
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = q.id)
ORDER BY q.created_at DESC, q.id DESC;

-- name: ListMostActivePlayers :many
-- Returns the players with the most finished games in the last 30
-- days, descending. The finished definition matches
-- ListPopularQuizzes and ListAnswersForQuizLeaderboard, and the
-- 30-day window matches ListPopularQuizzes -- without it the home
-- page's two leaderboards disagreed on what "recently active" meant
-- and a long-dormant player could outrank a current one (#355).
--
-- Anonymous (auto-petname) players are filtered out via
-- display_name_claimed = 1 so the public list only shows names a player
-- deliberately picked. The start-page leaderboard would otherwise be
-- cluttered with throwaway "happy-banana-xyz" entries from one-shot
-- visitors.
--
-- The EXISTS gate on questions excludes empty-quiz "plays" for the
-- same reason as ListPopularQuizzes above (#275).
--
-- LIMIT applied by the caller; see ListPopularQuizzes above for why.
SELECT p.id              AS id,
       p.display_name        AS display_name,
       COUNT(DISTINCT g.id) AS finished_count
FROM players p
JOIN game_participants gp ON gp.player_id = p.id
JOIN games g ON g.id = gp.game_id
WHERE g.created_at >= datetime('now', '-30 days')
  AND p.display_name_claimed = 1
  AND g.is_preview = 0
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = g.quiz_id)
  AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
GROUP BY p.id
ORDER BY finished_count DESC, p.display_name ASC;
