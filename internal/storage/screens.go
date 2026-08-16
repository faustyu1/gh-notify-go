package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/domain"
)

func (s *Store) InstallationsForUser(
	ctx context.Context, userID int64,
) ([]domain.Installation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, github_installation_id, account_login, account_type,
		       suspended_at IS NOT NULL
		FROM installations WHERE user_id = $1
		ORDER BY account_login`, userID)
	if err != nil {
		return nil, fmt.Errorf("query installations: %w", err)
	}
	defer rows.Close()

	var out []domain.Installation
	for rows.Next() {
		var it domain.Installation
		if err := rows.Scan(&it.ID, &it.GitHubInstallationID,
			&it.AccountLogin, &it.AccountType, &it.Suspended); err != nil {
			return nil, fmt.Errorf("scan installation: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) InstallationByID(ctx context.Context, id int64) (domain.Installation, error) {
	var it domain.Installation
	err := s.pool.QueryRow(ctx, `
		SELECT id, github_installation_id, account_login, account_type,
		       suspended_at IS NOT NULL
		FROM installations WHERE id = $1`, id).
		Scan(&it.ID, &it.GitHubInstallationID, &it.AccountLogin,
			&it.AccountType, &it.Suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, fmt.Errorf("installation %d not found", id)
	}
	if err != nil {
		return domain.Installation{}, fmt.Errorf("load installation: %w", err)
	}
	return it, nil
}

// ChatsForUser lists chats where this user set something up, not every chat
// the bot is in — the screen must not leak other people's chats.
func (s *Store) ChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.telegram_chat_id, c.title, count(i.id)
		FROM chats c
		JOIN integrations i ON i.chat_id = c.id
		WHERE i.created_by_user_id = $1
		GROUP BY c.id, c.telegram_chat_id, c.title
		ORDER BY c.title`, userID)
	if err != nil {
		return nil, fmt.Errorf("query chats: %w", err)
	}
	defer rows.Close()

	var out []domain.ChatSummary
	for rows.Next() {
		var it domain.ChatSummary
		if err := rows.Scan(&it.ChatID, &it.TelegramChatID,
			&it.Title, &it.IntegrationCount); err != nil {
			return nil, fmt.Errorf("scan chat summary: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) AddChatManager(ctx context.Context, chatID, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_managers (chat_id, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, chatID, userID)
	if err != nil {
		return fmt.Errorf("add chat manager: %w", err)
	}
	return nil
}

// CandidateChatsForUser lists chats this user may connect a repository to:
// the ones they brought the bot into, plus any they already have an
// integration in. Telegram admin rights are re-checked at connect time, so
// this list only has to be a safe superset — never every chat the bot knows.
func (s *Store) CandidateChatsForUser(
	ctx context.Context, userID int64,
) ([]domain.ChatSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.telegram_chat_id, c.title,
		       count(i.id) FILTER (WHERE i.created_by_user_id = $1)
		FROM chats c
		LEFT JOIN integrations i ON i.chat_id = c.id
		WHERE c.id IN (
			SELECT chat_id FROM chat_managers WHERE user_id = $1
			UNION
			SELECT chat_id FROM integrations WHERE created_by_user_id = $1
		)
		GROUP BY c.id, c.telegram_chat_id, c.title
		ORDER BY c.title`, userID)
	if err != nil {
		return nil, fmt.Errorf("query candidate chats: %w", err)
	}
	defer rows.Close()

	var out []domain.ChatSummary
	for rows.Next() {
		var it domain.ChatSummary
		if err := rows.Scan(&it.ChatID, &it.TelegramChatID,
			&it.Title, &it.IntegrationCount); err != nil {
			return nil, fmt.Errorf("scan candidate chat: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CountsForUser(ctx context.Context, userID int64) (int, int, int, error) {
	var accounts, repos, chats int
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM installations WHERE user_id = $1),
			(SELECT count(*) FROM integrations WHERE created_by_user_id = $1),
			(SELECT count(DISTINCT chat_id) FROM integrations WHERE created_by_user_id = $1)
	`, userID).Scan(&accounts, &repos, &chats)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count for user: %w", err)
	}
	return accounts, repos, chats, nil
}

func (s *Store) ClearTopic(ctx context.Context, telegramChatID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chats SET topic_id = NULL WHERE telegram_chat_id = $1`, telegramChatID)
	if err != nil {
		return fmt.Errorf("clear topic: %w", err)
	}
	return nil
}

func (s *Store) MarkIntegrationBroken(ctx context.Context, integrationID int64, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE integrations SET broken_reason = $2 WHERE id = $1`, integrationID, reason)
	if err != nil {
		return fmt.Errorf("mark integration broken: %w", err)
	}
	return nil
}
