-- +goose Up
-- +goose StatementBegin
-- #716: live sessions show the player's CURRENT players.display_name
-- everywhere, so a rename propagates (matching the solo leaderboard). The
-- per-session display_name snapshot on session_players is dropped: the roster,
-- standings, and quiz-leaderboard reads now join players and select
-- p.display_name. Dropping the column means per-session name uniqueness goes
-- too, so the UNIQUE (session_id, display_name) constraint is gone; the names
-- now live on players, whose own UNIQUE handles collisions.
--
-- session_players is a CHILD table - nothing FK-references it (session_answers
-- references sessions + players, not the roster) - so a plain table rebuild
-- under defer_foreign_keys is enough. The pragma postpones FK validation to
-- COMMIT so the DROP/RENAME ordering does not trip the session_players ->
-- sessions / players references. Ids are copied exactly and the surviving
-- UNIQUE (session_id, player_id) is re-declared inline.
PRAGMA defer_foreign_keys = ON;

CREATE TABLE session_players_new
(
    id           INTEGER PRIMARY KEY,
    session_id   TEXT     NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    player_id    INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    is_ready     INTEGER  NOT NULL DEFAULT 0,
    joined_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at      DATETIME,
    UNIQUE (session_id, player_id)
);

INSERT INTO session_players_new (id, session_id, player_id, is_ready, joined_at, last_seen_at, left_at)
SELECT id, session_id, player_id, is_ready, joined_at, last_seen_at, left_at
FROM session_players;

DROP TABLE session_players;
ALTER TABLE session_players_new RENAME TO session_players;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The dropped display_name values are gone for good, so the down can only
-- re-add the column (defaulting to '' so existing rows stay valid). The
-- UNIQUE (session_id, display_name) constraint is NOT restored: re-adding it
-- over '' defaults would collide on the second roster row of any session.
PRAGMA defer_foreign_keys = ON;

CREATE TABLE session_players_old
(
    id           INTEGER PRIMARY KEY,
    session_id   TEXT     NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    player_id    INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    display_name TEXT     NOT NULL DEFAULT '',
    is_ready     INTEGER  NOT NULL DEFAULT 0,
    joined_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at      DATETIME,
    UNIQUE (session_id, player_id)
);

INSERT INTO session_players_old (id, session_id, player_id, is_ready, joined_at, last_seen_at, left_at)
SELECT id, session_id, player_id, is_ready, joined_at, last_seen_at, left_at
FROM session_players;

DROP TABLE session_players;
ALTER TABLE session_players_old RENAME TO session_players;
-- +goose StatementEnd
