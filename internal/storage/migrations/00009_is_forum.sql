-- +goose Up
-- A supergroup is only a topic picker candidate when it is a forum. Telegram
-- reports is_forum on the chat at the moment the bot is added, so it is cached
-- here rather than guessed from the kind alone.
ALTER TABLE chats ADD COLUMN is_forum BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE chats DROP COLUMN is_forum;
