package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/domain"
)

type Filter struct {
	ID    int64
	Kind  string // author | branch | label | action
	Value string
}

// ChatByTelegramID loads one chat; the chat_detail and mute screens need the
// current mute window and topic in a single query.
func (s *Store) ChatByTelegramID(ctx context.Context, telegramChatID int64) (domain.Chat, error) {
	var c domain.Chat
	err := s.pool.QueryRow(ctx, `
		SELECT id, telegram_chat_id, title, kind, topic_id, muted_until
		FROM chats WHERE telegram_chat_id = $1`, telegramChatID).
		Scan(&c.ID, &c.TelegramChatID, &c.Title, &c.Kind, &c.TopicID, &c.MutedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Chat{}, fmt.Errorf("chat %d not found", telegramChatID)
	}
	if err != nil {
		return domain.Chat{}, fmt.Errorf("load chat: %w", err)
	}
	return c, nil
}

// TelegramChatForIntegration resolves the chat an integration belongs to.
// Authorization uses it instead of the chat id carried in callback params: a
// param is a claim, this is the fact.
func (s *Store) TelegramChatForIntegration(
	ctx context.Context, integrationID int64,
) (int64, error) {
	var telegramChatID int64
	err := s.pool.QueryRow(ctx, `
		SELECT c.telegram_chat_id
		FROM integrations i
		JOIN chats c ON c.id = i.chat_id
		WHERE i.id = $1`, integrationID).Scan(&telegramChatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("integration %d not found", integrationID)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve integration chat: %w", err)
	}
	return telegramChatID, nil
}

// TelegramChatForFilter does the same for a filter row, which reaches the
// handler with nothing but its own id.
func (s *Store) TelegramChatForFilter(ctx context.Context, filterID int64) (int64, error) {
	var telegramChatID int64
	err := s.pool.QueryRow(ctx, `
		SELECT c.telegram_chat_id
		FROM filters f
		JOIN integrations i ON i.id = f.integration_id
		JOIN chats c ON c.id = i.chat_id
		WHERE f.id = $1`, filterID).Scan(&telegramChatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("filter %d not found", filterID)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve filter chat: %w", err)
	}
	return telegramChatID, nil
}

// SetChatMute sets or clears a chat's mute window. A muted chat receives
// nothing: the worker-side fanout query drops it.
func (s *Store) SetChatMute(ctx context.Context, telegramChatID int64, until *time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chats SET muted_until = $2 WHERE telegram_chat_id = $1`,
		telegramChatID, until)
	if err != nil {
		return fmt.Errorf("set chat mute: %w", err)
	}
	return nil
}

// SetChatTopic points a chat's deliveries at a forum topic; nil clears it
// back to the General topic.
func (s *Store) SetChatTopic(ctx context.Context, telegramChatID int64, topicID *int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chats SET topic_id = $2 WHERE telegram_chat_id = $1`,
		telegramChatID, topicID)
	if err != nil {
		return fmt.Errorf("set chat topic: %w", err)
	}
	return nil
}

// IntegrationsInChat lists everything wired to one chat, broken ones last.
func (s *Store) IntegrationsInChat(ctx context.Context, chatID int64) ([]domain.Integration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.chat_id, c.telegram_chat_id, c.topic_id,
		       i.installation_id, ins.github_installation_id,
		       i.repo_github_id, i.repo_full_name,
		       i.created_by_user_id, u.telegram_id, i.broken_reason
		FROM integrations i
		JOIN chats c ON c.id = i.chat_id
		JOIN installations ins ON ins.id = i.installation_id
		JOIN users u ON u.id = i.created_by_user_id
		WHERE i.chat_id = $1
		ORDER BY i.broken_reason IS NOT NULL, i.repo_full_name`, chatID)
	if err != nil {
		return nil, fmt.Errorf("query chat integrations: %w", err)
	}
	defer rows.Close()

	var out []domain.Integration
	for rows.Next() {
		var it domain.Integration
		if err := rows.Scan(
			&it.ID, &it.ChatID, &it.TelegramChatID, &it.TopicID,
			&it.InstallationID, &it.GitHubInstallationID,
			&it.RepoGitHubID, &it.RepoFullName,
			&it.CreatedByUserID, &it.OwnerTelegramID, &it.BrokenReason,
		); err != nil {
			return nil, fmt.Errorf("scan chat integration: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIntegration(ctx context.Context, integrationID int64) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM integrations WHERE id = $1`, integrationID); err != nil {
		return fmt.Errorf("delete integration: %w", err)
	}
	return nil
}

// EventSettings returns explicit per-kind settings for an integration. Kinds
// missing from the map default to enabled.
func (s *Store) EventSettings(ctx context.Context, integrationID int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT event_kind, enabled FROM event_settings WHERE integration_id = $1`,
		integrationID)
	if err != nil {
		return nil, fmt.Errorf("query event settings: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var kind string
		var enabled bool
		if err := rows.Scan(&kind, &enabled); err != nil {
			return nil, fmt.Errorf("scan event setting: %w", err)
		}
		out[kind] = enabled
	}
	return out, rows.Err()
}

// SetEventEnabled upserts one kind's toggle.
func (s *Store) SetEventEnabled(ctx context.Context, integrationID int64, kind string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO event_settings (integration_id, event_kind, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (integration_id, event_kind) DO UPDATE SET enabled = EXCLUDED.enabled`,
		integrationID, kind, enabled)
	if err != nil {
		return fmt.Errorf("set event enabled: %w", err)
	}
	return nil
}

// IntegrationOwner finds who to tell when delivery to one of their
// integrations fails terminally, and in which language to tell them.
func (s *Store) IntegrationOwner(
	ctx context.Context, integrationID int64,
) (telegramID int64, repoFullName, language string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT u.telegram_id, i.repo_full_name, u.language
		FROM integrations i
		JOIN users u ON u.id = i.created_by_user_id
		WHERE i.id = $1`, integrationID).Scan(&telegramID, &repoFullName, &language)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", "", fmt.Errorf("integration %d not found", integrationID)
	}
	if err != nil {
		return 0, "", "", fmt.Errorf("load integration owner: %w", err)
	}
	return telegramID, repoFullName, language, nil
}

// WriteAudit records an admin action; meta may be nil.
func (s *Store) WriteAudit(
	ctx context.Context, actorUserID int64, chatID *int64, action string, meta map[string]any,
) error {
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode audit meta: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, chat_id, action, meta)
		VALUES ($1, $2, $3, $4)`, actorUserID, chatID, action, raw)
	if err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}

// IntegrationHealth is the per-integration view for the health screen.
type IntegrationHealth struct {
	RepoFullName string
	ChatTitle    string
	LastEventAt  *time.Time
	Sent24h      int
	Failed24h    int
	BrokenReason *string
	MutedUntil   *time.Time
}

func (s *Store) HealthForIntegration(
	ctx context.Context, integrationID int64,
) (IntegrationHealth, error) {
	var h IntegrationHealth
	err := s.pool.QueryRow(ctx, `
		SELECT i.repo_full_name, c.title, i.last_event_at, i.broken_reason, c.muted_until,
		       (SELECT count(*) FROM outbox o WHERE o.integration_id = i.id
		         AND o.status = 'sent' AND o.created_at > now() - interval '24 hours'),
		       (SELECT count(*) FROM outbox o WHERE o.integration_id = i.id
		         AND o.status = 'failed' AND o.created_at > now() - interval '24 hours')
		FROM integrations i
		JOIN chats c ON c.id = i.chat_id
		WHERE i.id = $1`, integrationID).
		Scan(&h.RepoFullName, &h.ChatTitle, &h.LastEventAt, &h.BrokenReason,
			&h.MutedUntil, &h.Sent24h, &h.Failed24h)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntegrationHealth{}, fmt.Errorf("integration %d not found", integrationID)
	}
	if err != nil {
		return IntegrationHealth{}, fmt.Errorf("load health: %w", err)
	}
	return h, nil
}

func (s *Store) AddFilter(ctx context.Context, integrationID int64, kind, value string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, $2, $3)
		RETURNING id`, integrationID, kind, value).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("add filter: %w", err)
	}
	return id, nil
}

func (s *Store) DeleteFilter(ctx context.Context, filterID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM filters WHERE id = $1`, filterID); err != nil {
		return fmt.Errorf("delete filter: %w", err)
	}
	return nil
}

func (s *Store) FiltersForIntegration(ctx context.Context, integrationID int64) ([]Filter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, pattern FROM filters
		WHERE integration_id = $1 ORDER BY id`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("query filters: %w", err)
	}
	defer rows.Close()

	var out []Filter
	for rows.Next() {
		var f Filter
		if err := rows.Scan(&f.ID, &f.Kind, &f.Value); err != nil {
			return nil, fmt.Errorf("scan filter: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// StatusStats summarises a user's delivery health over the last 24 hours.
func (s *Store) StatusStats(ctx context.Context, userID int64) (sent, failed int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE o.status = 'sent'),
			count(*) FILTER (WHERE o.status = 'failed')
		FROM outbox o
		JOIN integrations i ON i.id = o.integration_id
		WHERE i.created_by_user_id = $1
		  AND o.created_at > now() - interval '24 hours'`, userID).
		Scan(&sent, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("status stats: %w", err)
	}
	return sent, failed, nil
}
