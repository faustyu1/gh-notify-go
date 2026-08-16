package storage

import (
	"context"
	"fmt"
)

func (s *Store) UpsertUser(ctx context.Context, telegramID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (telegram_id) VALUES ($1)
		ON CONFLICT (telegram_id) DO UPDATE SET telegram_id = EXCLUDED.telegram_id
		RETURNING id`, telegramID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert user: %w", err)
	}
	return id, nil
}

// UpsertChat refreshes the cached title on every call, because a group can be
// renamed and a stale title in the chat picker is confusing.
func (s *Store) UpsertChat(ctx context.Context, telegramChatID int64, title, kind string) (int64, error) {
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

func (s *Store) UpsertInstallation(
	ctx context.Context, githubInstallationID int64, login, accountType string, userID int64,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO installations
			(github_installation_id, account_login, account_type, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (github_installation_id) DO UPDATE
		SET account_login = EXCLUDED.account_login,
		    account_type  = EXCLUDED.account_type,
		    user_id       = EXCLUDED.user_id,
		    suspended_at  = NULL
		RETURNING id`, githubInstallationID, login, accountType, userID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert installation: %w", err)
	}
	return id, nil
}
