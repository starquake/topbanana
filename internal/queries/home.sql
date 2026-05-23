-- name: ListPopularQuizzes :many
-- Returns the most-played quizzes over the last 30 days, scored by number of
-- finished games. A game counts as finished when every quiz question has
-- been issued, i.e. the count of game_questions rows for the game has
-- caught up with the count of questions on the quiz. Same finisher
-- condition as ListAnswersForQuizLeaderboard so the home page and the
-- per-quiz leaderboard agree on what "played" means.
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
-- player, neither of which fits this anonymous list.
SELECT q.id          AS id,
       q.title       AS title,
       q.slug        AS slug,
       q.description AS description,
       q.updated_at  AS updated_at,
       COUNT(DISTINCT g.id) AS play_count
FROM quizzes q
JOIN games g ON g.quiz_id = q.id
WHERE g.created_at >= datetime('now', '-30 days')
  AND q.visibility = 'public'
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = q.id)
  AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = q.id)
GROUP BY q.id
ORDER BY play_count DESC, q.updated_at DESC;

-- name: ListMostActivePlayers :many
-- Returns the players with the most finished games, descending. The
-- finished definition matches ListPopularQuizzes and
-- ListAnswersForQuizLeaderboard.
--
-- Anonymous (auto-petname) players are filtered out via
-- username_claimed = 1 so the public list only shows names a player
-- deliberately picked. The start-page leaderboard would otherwise be
-- cluttered with throwaway "happy-banana-xyz" entries from one-shot
-- visitors.
--
-- The EXISTS gate on questions excludes empty-quiz "plays" for the
-- same reason as ListPopularQuizzes above (#275).
--
-- LIMIT applied by the caller; see ListPopularQuizzes above for why.
SELECT p.id              AS id,
       p.username        AS username,
       COUNT(DISTINCT g.id) AS finished_count
FROM players p
JOIN game_participants gp ON gp.player_id = p.id
JOIN games g ON g.id = gp.game_id
WHERE p.username_claimed = 1
  AND EXISTS (SELECT 1 FROM questions qe WHERE qe.quiz_id = g.quiz_id)
  AND (SELECT COUNT(*) FROM game_questions gq WHERE gq.game_id = g.id) >=
      (SELECT COUNT(*) FROM questions qc WHERE qc.quiz_id = g.quiz_id)
GROUP BY p.id
ORDER BY finished_count DESC, p.username ASC;
