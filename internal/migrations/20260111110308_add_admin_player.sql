-- +goose Up
-- +goose StatementBegin
INSERT INTO players (id, username, email, created_at)
VALUES (1, 'admin', 'email@example.com', CURRENT_TIMESTAMP);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM players WHERE id = 1;
-- +goose StatementEnd
