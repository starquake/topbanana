-- +goose Up
-- +goose StatementBegin
-- MP-1 (#678): hosted live sessions. sessions and session_players are
-- new CHILD tables (nothing FK-references them yet), so a plain CREATE
-- TABLE inside goose's default transaction is enough - no table-rebuild
-- or FK-defer dance.
--
-- A session is one hosted run of a live quiz. The host is the logged-in
-- host/admin who opened it; host_player_id FK-references players. join_code
-- is the short, ambiguity-free room code players type or scan; UNIQUE so a
-- code resolves to exactly one open session. phase is the server-authoritative
-- state-machine label; only 'lobby' exists this phase (MP-1), the CHECK leaves
-- room for the later gameplay phases (MP-5).
--
-- Timing/gameplay columns (current_round_id, question_started_at, ...) are
-- deferred to MP-5 - the lobby does not need them. started_at / finished_at
-- are included now because the lobby's create/start boundary is meaningful
-- and cheap to carry; both stay NULL until a later phase stamps them.
CREATE TABLE sessions
(
    id             TEXT PRIMARY KEY,
    quiz_id        INTEGER  NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
    host_player_id INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    join_code      TEXT     NOT NULL UNIQUE,
    phase          TEXT     NOT NULL DEFAULT 'lobby' CHECK (phase IN ('lobby')),
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at     DATETIME,
    finished_at    DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
-- session_players is the roster: one row per player who joined a session.
-- player_id FK-references players (joiners reuse the anonymous-player model).
-- display_name is the name shown in the lobby; it is denormalised onto the
-- roster row rather than read off players so a per-session display-name
-- collision can fall back to a petname without renaming the underlying
-- player row. is_ready is the lobby ready toggle. left_at stays NULL until
-- a player leaves (MP-10); last_seen_at backs the future heartbeat/active
-- definition (MP-10) but is stamped at join now so the column is never NULL.
--
-- UNIQUE (session_id, player_id) keeps one roster row per player per session
-- so a re-join is an update, not a duplicate. UNIQUE (session_id, display_name)
-- enforces per-session display-name uniqueness at the DB level; the join path
-- falls back to a petname on the collision.
CREATE TABLE session_players
(
    id           INTEGER PRIMARY KEY,
    session_id   TEXT     NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    player_id    INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    display_name TEXT     NOT NULL,
    is_ready     INTEGER  NOT NULL DEFAULT 0,
    joined_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at      DATETIME,
    UNIQUE (session_id, player_id),
    UNIQUE (session_id, display_name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE session_players;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd
