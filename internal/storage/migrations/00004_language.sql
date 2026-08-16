-- +goose Up
-- Every user-visible string is localized: users.language drives the private
-- UI, chats.language drives notification rendering for the group. The
-- language is seeded from Telegram's language_code when the row first
-- appears and can be changed explicitly afterwards.

ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT 'en';
ALTER TABLE chats ADD COLUMN language TEXT NOT NULL DEFAULT 'en';

-- +goose Down
ALTER TABLE users DROP COLUMN language;
ALTER TABLE chats DROP COLUMN language;
