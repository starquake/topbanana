-- +goose Up
-- +goose StatementBegin
-- #358: sweep orphan game / game_question / game_answer rows left
-- behind by the participant dedup in migration 20260520180000. That
-- migration kept the lowest game_participants.id per (player, quiz)
-- but explicitly left the dropped duplicates' parent games and their
-- dependent rows in place ("nothing player-keyed queries them after
-- the dedup. A future cleanup migration can sweep them.") — this is
-- that migration.
--
-- The orphans matter because ListPopularQuizzes joins on `games`
-- (not `game_participants`) and still counts them in play_count,
-- inflating popularity. They also bloat scan time and confuse any
-- analytics query reading the raw tables.
--
-- Delete in dependency order: answers → questions → games. The
-- subselect identifies orphan game IDs once; doing it inline three
-- times would re-scan each delete.

DELETE FROM game_answers
WHERE game_id IN (
    SELECT g.id FROM games g
    LEFT JOIN game_participants gp ON gp.game_id = g.id
    WHERE gp.id IS NULL
);

DELETE FROM game_questions
WHERE game_id IN (
    SELECT g.id FROM games g
    LEFT JOIN game_participants gp ON gp.game_id = g.id
    WHERE gp.id IS NULL
);

DELETE FROM games
WHERE id IN (
    SELECT g.id FROM games g
    LEFT JOIN game_participants gp ON gp.game_id = g.id
    WHERE gp.id IS NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- One-way migration: reconstructing the deleted orphan rows would
-- need a snapshot we did not take. The down direction is intentionally
-- a no-op; rolling back the index drops are covered by the sibling
-- migrations in this bundle.
SELECT 1;
-- +goose StatementEnd
