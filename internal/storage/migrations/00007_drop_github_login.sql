-- +goose Up
-- users.github_login was never written and never read: a user is identified by
-- their Telegram id, and the GitHub side is reached through installations. The
-- column only invited the assumption that the mapping exists.
ALTER TABLE users DROP COLUMN github_login;

-- +goose Down
ALTER TABLE users ADD COLUMN github_login TEXT;
