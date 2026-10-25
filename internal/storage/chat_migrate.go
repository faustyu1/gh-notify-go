package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MigrateChat follows a group that Telegram upgraded to a supergroup. The
// upgrade hands the chat a new id and the old one starts failing every call
// with "group chat was upgraded to a supergroup chat", so without this every
// integration in the chat goes silent and every connect fails.
//
// The old row keeps its internal id, which screens and nav stacks already
// point at. If the bot has meanwhile seen the supergroup under its new id
// (being re-added, a topic message), that duplicate row is folded into the
// old one: its managers, topics and integrations move over, and anything
// that would collide with what the old row already has is dropped.
//
// A chat the bot has no row for under the old id is left alone.
func (s *Store) MigrateChat(ctx context.Context, oldTelegramID, newTelegramID int64) error {
	if oldTelegramID == newTelegramID {
		return nil
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var oldID int64
		err := tx.QueryRow(ctx,
			`SELECT id FROM chats WHERE telegram_chat_id = $1 FOR UPDATE`,
			oldTelegramID).Scan(&oldID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		var (
			newID   int64
			title   string
			isForum bool
		)
		err = tx.QueryRow(ctx,
			`SELECT id, title, is_forum FROM chats WHERE telegram_chat_id = $1 FOR UPDATE`,
			newTelegramID).Scan(&newID, &title, &isForum)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx, `
				UPDATE chats SET telegram_chat_id = $2, kind = 'supergroup'
				WHERE id = $1`, oldID, newTelegramID)
			return err
		case err != nil:
			return err
		}

		merge := []string{
			`INSERT INTO chat_managers (chat_id, user_id, created_at)
			 SELECT $1::bigint, user_id, created_at FROM chat_managers WHERE chat_id = $2
			 ON CONFLICT DO NOTHING`,
			`INSERT INTO chat_topics (chat_id, topic_id, title, seen_at)
			 SELECT $1::bigint, topic_id, title, seen_at FROM chat_topics WHERE chat_id = $2
			 ON CONFLICT DO NOTHING`,
			`UPDATE integrations i SET chat_id = $1
			 WHERE i.chat_id = $2 AND NOT EXISTS (
			     SELECT 1 FROM integrations o
			     WHERE o.chat_id = $1 AND o.installation_id = i.installation_id
			       AND o.repo_github_id = i.repo_github_id)`,
			`UPDATE outbox SET chat_id = $1 WHERE chat_id = $2`,
			`UPDATE audit_log SET chat_id = $1 WHERE chat_id = $2`,
		}
		for _, q := range merge {
			if _, err := tx.Exec(ctx, q, oldID, newID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM chats WHERE id = $1`, newID); err != nil {
			return err
		}

		// The duplicate was written from the supergroup itself, so its title
		// and forum flag are the current ones.
		_, err = tx.Exec(ctx, `
			UPDATE chats SET telegram_chat_id = $2, kind = 'supergroup',
			                 title = $3, is_forum = $4
			WHERE id = $1`, oldID, newTelegramID, title, isForum)
		return err
	})
	if err != nil {
		return fmt.Errorf("migrate chat: %w", err)
	}
	return nil
}
