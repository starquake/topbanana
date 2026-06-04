-- +goose Up
-- +goose StatementBegin
-- MP-0 (#677): quiz play mode. NOT NULL with DEFAULT 'solo' so existing
-- rows stay solo-playable; the CHECK enforces the same set the admin
-- form's selector exposes. A constant-default ADD COLUMN with a CHECK
-- needs no table rebuild, so there is no FK-rebuild dance here.
--
-- A 'live' quiz is hosted-only (MP-1+): it is excluded from the solo
-- browse lists and rejected by the solo play / game-create path.
ALTER TABLE quizzes ADD COLUMN mode TEXT NOT NULL DEFAULT 'solo'
    CHECK (mode IN ('solo', 'live'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE quizzes DROP COLUMN mode;
-- +goose StatementEnd
