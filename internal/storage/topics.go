package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ChatTopic is one forum topic the bot has seen in a chat. Title is empty
// when the topic existed before the bot did and nothing has revealed its
// name yet; the picker falls back to the id in that case.
type ChatTopic struct {
	TopicID int64
	Title   string
}

// RecordChatTopic remembers a topic seen in a chat. An empty title never
// overwrites a known one: a plain message inside a topic proves the topic
// exists but says nothing about its name.
//
// A chat the bot has no row for records nothing, which is deliberate: the
// picker only ever offers chats that reached the database through the normal
// "bot was added" path.
func (s *Store) RecordChatTopic(
	ctx context.Context, telegramChatID, topicID int64, title string,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_topics (chat_id, topic_id, title)
		SELECT id, $2, $3 FROM chats WHERE telegram_chat_id = $1
		ON CONFLICT (chat_id, topic_id) DO UPDATE
		SET title   = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title
		                   ELSE chat_topics.title END,
		    seen_at = now()`,
		telegramChatID, topicID, title)
	if err != nil {
		return fmt.Errorf("record chat topic: %w", err)
	}
	return nil
}

// ForgetChatTopic drops a topic that can no longer receive messages — closed
// or deleted — so the picker stops offering it.
func (s *Store) ForgetChatTopic(ctx context.Context, telegramChatID, topicID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM chat_topics ct
		USING chats c
		WHERE ct.chat_id = c.id AND c.telegram_chat_id = $1 AND ct.topic_id = $2`,
		telegramChatID, topicID)
	if err != nil {
		return fmt.Errorf("forget chat topic: %w", err)
	}
	return nil
}

// TopicsForChat lists what the picker offers, named topics first so the ones
// with nothing but an id sink to the bottom.
func (s *Store) TopicsForChat(ctx context.Context, chatID int64) ([]ChatTopic, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT topic_id, title FROM chat_topics
		WHERE chat_id = $1
		ORDER BY title = '', title, topic_id`, chatID)
	if err != nil {
		return nil, fmt.Errorf("query chat topics: %w", err)
	}
	defer rows.Close()

	var out []ChatTopic
	for rows.Next() {
		var t ChatTopic
		if err := rows.Scan(&t.TopicID, &t.Title); err != nil {
			return nil, fmt.Errorf("scan chat topic: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreatorTelegramForIntegration answers "who connected this repository" in
// the only terms authorization can use: a telegram user id. Disconnecting is
// limited to that admin and to the chat's owner.
func (s *Store) CreatorTelegramForIntegration(
	ctx context.Context, integrationID int64,
) (int64, error) {
	var telegramID int64
	err := s.pool.QueryRow(ctx, `
		SELECT u.telegram_id
		FROM integrations i
		JOIN users u ON u.id = i.created_by_user_id
		WHERE i.id = $1`, integrationID).Scan(&telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("integration %d not found", integrationID)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve integration creator: %w", err)
	}
	return telegramID, nil
}
