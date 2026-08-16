-- +goose Up
-- Notification language is resolved from the integration's owner at claim
-- time (users.language), so a chat no longer carries a language of its own:
-- two deliveries in one chat can each speak their owner's language.
ALTER TABLE chats DROP COLUMN language;

-- +goose Down
ALTER TABLE chats ADD COLUMN language TEXT NOT NULL DEFAULT 'en';
