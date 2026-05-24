-- +goose Up
-- player_identities maps an external identity provider's stable user id
-- (subject) onto a local players row. Keyed by (provider, subject) so the
-- same person who signs in with Google and later with GitHub gets two
-- rows pointing at one players.id. ON DELETE CASCADE keeps the join
-- table tidy when a player row is removed; the provider/subject pair is
-- unique across the table because no two providers should ever stamp
-- the same (provider, subject) onto distinct players.
-- +goose StatementBegin
CREATE TABLE player_identities
(
    id         INTEGER  PRIMARY KEY,
    player_id  INTEGER  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    provider   TEXT     NOT NULL,
    subject    TEXT     NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (provider, subject)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX player_identities_player_id_idx ON player_identities (player_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX player_identities_player_id_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE player_identities;
-- +goose StatementEnd
