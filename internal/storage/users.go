package storage

import (
	"context"
	"fmt"
)

// UpsertUser returns the internal id and the stored language. The language is
// seeded from Telegram's language_code on the first insert only: once the
// user has picked a language in the settings screen, later callbacks must
// not reset it back to the client's locale.
func (s *Store) UpsertUser(
	ctx context.Context, telegramID int64, language string,
) (id int64, storedLanguage string, err error) {
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (telegram_id, language) VALUES ($1, $2)
		ON CONFLICT (telegram_id) DO UPDATE SET telegram_id = EXCLUDED.telegram_id
		RETURNING id, language`, telegramID, language).Scan(&id, &storedLanguage)
	if err != nil {
		return 0, "", fmt.Errorf("upsert user: %w", err)
	}
	return id, storedLanguage, nil
}

// UpsertChat refreshes the cached title on every call, because a group can be
// renamed and a stale title in the chat picker is confusing. A chat has no
// language of its own: notifications resolve the integration owner's locale
// at claim time.
func (s *Store) UpsertChat(
	ctx context.Context, telegramChatID int64, title, kind string,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chats (telegram_chat_id, title, kind) VALUES ($1, $2, $3)
		ON CONFLICT (telegram_chat_id)
		DO UPDATE SET title = EXCLUDED.title, kind = EXCLUDED.kind
		RETURNING id`, telegramChatID, title, kind).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert chat: %w", err)
	}
	return id, nil
}

// SetUserLanguage records an explicit language choice for the private UI and
// for the user's integrations' notifications.
func (s *Store) SetUserLanguage(ctx context.Context, userID int64, language string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET language = $2 WHERE id = $1`, userID, language)
	if err != nil {
		return fmt.Errorf("set user language: %w", err)
	}
	return nil
}
