-- +goose Up
-- +goose StatementBegin
-- Optional per-round override for the round-boundary auto-advance window
-- (intro and recap/results cards). NULL means "inherit the quiz default";
-- the game service resolves round -> quiz -> defaultExpiration at boundary
-- time. CHECK clamps to a sane range as a DB-level backstop against a bogus
-- manual UPDATE; the admin form enforces the same bounds. A nullable,
-- constant-default-free ADD COLUMN needs no table rebuild, so there is no
-- FK-rebuild dance here even though rounds is a parent table.
ALTER TABLE rounds ADD COLUMN boundary_duration_seconds INTEGER NULL
    CHECK (boundary_duration_seconds IS NULL OR boundary_duration_seconds BETWEEN 1 AND 600);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE rounds DROP COLUMN boundary_duration_seconds;
-- +goose StatementEnd
