-- +goose Up
-- The one-attempt-per-(player, quiz) rule was previously enforced by a
-- check-then-insert in Service.CreateGame with no surrounding transaction
-- and no DB constraint, so two concurrent CreateGame calls for the same
-- (player, quiz) could both pass the existence check and both insert.
-- See #273 for the audit.
--
-- This migration denormalises quiz_id onto game_participants and adds a
-- UNIQUE INDEX on (player_id, quiz_id). The loser of any concurrent
-- insert race now surfaces as a SQLite UNIQUE constraint failure that
-- the store maps to ErrGameAlreadyExists.
--
-- Done as a table rebuild rather than ALTER TABLE ADD COLUMN so the
-- ID-typed columns sit together in the schema (id, game_id, player_id,
-- quiz_id, joined_at) — easier to read at a glance than an appended
-- quiz_id trailing joined_at.
--
-- The INSERT carries the dedup that bug-era dev / staging DBs need:
-- keeping the lowest game_participants.id per (player_id, quiz_id)
-- pair. The dropped duplicates' parent games remain as orphan rows;
-- nothing player-keyed queries them after the dedup. A future cleanup
-- migration can sweep them.
--
-- defer_foreign_keys postpones FK validation to the end of the
-- transaction so the DROP/RENAME ordering inside the migration does
-- not trip the games -> game_participants reference (there isn't one
-- today, but the pragma is the right default for any table rebuild).
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;

CREATE TABLE game_participants_new
(
    id        INTEGER PRIMARY KEY,
    game_id   VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id INTEGER     NOT NULL REFERENCES players (id),
    quiz_id   INTEGER              REFERENCES quizzes (id),
    joined_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO game_participants_new (id, game_id, player_id, quiz_id, joined_at)
SELECT gp.id, gp.game_id, gp.player_id, g.quiz_id, gp.joined_at
FROM game_participants gp
         JOIN games g ON g.id = gp.game_id
WHERE gp.id IN (SELECT MIN(gp2.id)
                FROM game_participants gp2
                         JOIN games g2 ON g2.id = gp2.game_id
                GROUP BY gp2.player_id, g2.quiz_id);

DROP TABLE game_participants;
ALTER TABLE game_participants_new RENAME TO game_participants;

CREATE UNIQUE INDEX game_participants_player_quiz_idx
    ON game_participants (player_id, quiz_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;

DROP INDEX game_participants_player_quiz_idx;

CREATE TABLE game_participants_old
(
    id        INTEGER PRIMARY KEY,
    game_id   VARCHAR(20) NOT NULL REFERENCES games (id),
    player_id INTEGER     NOT NULL REFERENCES players (id),
    joined_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO game_participants_old (id, game_id, player_id, joined_at)
SELECT id, game_id, player_id, joined_at
FROM game_participants;

DROP TABLE game_participants;
ALTER TABLE game_participants_old RENAME TO game_participants;
-- +goose StatementEnd
