-- +goose Up
-- Telegram has no API for listing a forum's topics, so the bot remembers the
-- ones it sees: a topic-created service message, an edited name, or any
-- message posted inside a topic. That list is what the topic picker offers,
-- instead of asking an admin to type a numeric id nobody can look up.
CREATE TABLE chat_topics (
    chat_id  BIGINT      NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    topic_id BIGINT      NOT NULL,
    title    TEXT        NOT NULL DEFAULT '',
    seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, topic_id)
);

-- +goose Down
DROP TABLE chat_topics;
