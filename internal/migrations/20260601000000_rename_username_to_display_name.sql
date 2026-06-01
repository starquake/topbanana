-- +goose Up
ALTER TABLE players RENAME COLUMN username TO display_name;
ALTER TABLE players RENAME COLUMN username_claimed TO display_name_claimed;

-- +goose Down
ALTER TABLE players RENAME COLUMN display_name TO username;
ALTER TABLE players RENAME COLUMN display_name_claimed TO username_claimed;
