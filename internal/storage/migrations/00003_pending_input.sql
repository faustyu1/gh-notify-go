-- +goose Up
-- One pending ForceReply input per user: which action the next reply feeds
-- (set topic, add filter) and the params to apply it with.
ALTER TABLE ui_nav ADD COLUMN pending JSONB;

-- +goose Down
ALTER TABLE ui_nav DROP COLUMN pending;
