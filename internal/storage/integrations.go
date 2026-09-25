package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/domain"
)

func (s *Store) CreateIntegration(
	ctx context.Context, chatID, installationID, repoGitHubID int64,
	repoFullName string, createdByUserID int64,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO integrations
			(chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		chatID, installationID, repoGitHubID, repoFullName, createdByUserID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create integration: %w", err)
	}
	return id, nil
}

// IntegrationsForRepo returns every live integration a GitHub webhook should
// fan out to. Muted and broken ones are excluded here so the ingest path
// stays a single query.
func (s *Store) IntegrationsForRepo(
	ctx context.Context, repoGitHubID, githubInstallationID int64,
) ([]domain.Integration, error) {
	return s.liveIntegrations(ctx, `ins.github_installation_id = $2`,
		repoGitHubID, githubInstallationID)
}

// IntegrationsForProject is the webhook-connection side of
// IntegrationsForRepo. A delivery has already been authenticated to its
// connection, which is the internal installation id.
func (s *Store) IntegrationsForProject(
	ctx context.Context, installationID, projectID int64,
) ([]domain.Integration, error) {
	return s.liveIntegrations(ctx, `ins.id = $2 AND ins.provider <> 'github'`,
		projectID, installationID)
}

// liveIntegrations runs the fan-out query with the installation condition
// supplied by the caller; $1 is always the repository id.
func (s *Store) liveIntegrations(
	ctx context.Context, installationCond string, repoID, installation int64,
) ([]domain.Integration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.chat_id, c.telegram_chat_id, c.topic_id,
		       i.installation_id, COALESCE(ins.github_installation_id, 0), ins.provider,
		       i.repo_github_id, i.repo_full_name,
		       i.created_by_user_id, u.telegram_id
		FROM integrations i
		JOIN chats c          ON c.id  = i.chat_id
		JOIN installations ins ON ins.id = i.installation_id
		JOIN users u          ON u.id  = i.created_by_user_id
		WHERE i.repo_github_id = $1
		  AND `+installationCond+`
		  AND i.broken_reason IS NULL
		  AND (c.muted_until IS NULL OR c.muted_until <= now())`,
		repoID, installation)
	if err != nil {
		return nil, fmt.Errorf("query integrations: %w", err)
	}
	defer rows.Close()

	var out []domain.Integration
	for rows.Next() {
		var it domain.Integration
		if err := rows.Scan(
			&it.ID, &it.ChatID, &it.TelegramChatID, &it.TopicID,
			&it.InstallationID, &it.GitHubInstallationID, &it.Provider,
			&it.RepoGitHubID, &it.RepoFullName,
			&it.CreatedByUserID, &it.OwnerTelegramID,
		); err != nil {
			return nil, fmt.Errorf("scan integration: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// EventEnabled treats a missing row as enabled, so a fresh integration
// delivers everything and the settings table only stores deviations.
func (s *Store) EventEnabled(ctx context.Context, integrationID int64, kind string) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT enabled FROM event_settings
		WHERE integration_id = $1 AND event_kind = $2`, integrationID, kind).Scan(&enabled)

	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read event setting: %w", err)
	}
	return enabled, nil
}
