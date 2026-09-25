package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownWebhookToken is what an unauthenticated GitLab delivery gets: the
// token matches no connection.
var ErrUnknownWebhookToken = errors.New("unknown gitlab webhook token")

// GitLabProject is one project a GitLab connection has delivered events for.
type GitLabProject struct {
	ID     int64
	Path   string
	WebURL string
}

func hashWebhookToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// GitLabConnectionForSetup returns the connection the "Connect GitLab"
// button leads to: the user's connection that has not received anything yet,
// or a new one. Tapping the button twice must not leave a trail of unused
// tokens behind.
func (s *Store) GitLabConnectionForSetup(ctx context.Context, userID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT ins.id FROM installations ins
		WHERE ins.user_id = $1 AND ins.provider = 'gitlab'
		  AND NOT EXISTS (SELECT 1 FROM gitlab_projects p WHERE p.installation_id = ins.id)
		ORDER BY ins.id LIMIT 1`, userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("find unused gitlab connection: %w", err)
	}
	return s.CreateGitLabConnection(ctx, userID)
}

// CreateGitLabConnection mints a webhook token for a new GitLab connection
// owned by userID and returns the connection's id. The token is the only
// credential a GitLab delivery carries, so it is 32 random bytes.
func (s *Store) CreateGitLabConnection(ctx context.Context, userID int64) (int64, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return 0, fmt.Errorf("generate webhook token: %w", err)
	}
	token := hex.EncodeToString(buf)

	sealed, err := s.box.Seal([]byte(token))
	if err != nil {
		return 0, fmt.Errorf("seal webhook token: %w", err)
	}

	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO installations
			(provider, account_login, account_type, user_id,
			 webhook_token_hash, webhook_token_ciphertext)
		VALUES ('gitlab', '', 'gitlab', $1, $2, $3)
		RETURNING id`,
		userID, hashWebhookToken(token), sealed).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create gitlab connection: %w", err)
	}
	return id, nil
}

// GitLabWebhookToken reveals a connection's token to its owner, and to
// nobody else: the token is what lets anyone post events in their name.
func (s *Store) GitLabWebhookToken(
	ctx context.Context, installationID, userID int64,
) (string, error) {
	var sealed []byte
	err := s.pool.QueryRow(ctx, `
		SELECT webhook_token_ciphertext FROM installations
		WHERE id = $1 AND user_id = $2 AND provider = 'gitlab'`,
		installationID, userID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("gitlab connection %d not found", installationID)
	}
	if err != nil {
		return "", fmt.Errorf("load webhook token: %w", err)
	}
	token, err := s.box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("open webhook token: %w", err)
	}
	return string(token), nil
}

// GitLabInstallationByToken authenticates a GitLab delivery. The lookup is by
// hash, so the comparison never touches the token itself.
func (s *Store) GitLabInstallationByToken(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, ErrUnknownWebhookToken
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM installations
		WHERE webhook_token_hash = $1 AND provider = 'gitlab'`,
		hashWebhookToken(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUnknownWebhookToken
	}
	if err != nil {
		return 0, fmt.Errorf("find gitlab connection: %w", err)
	}
	return id, nil
}

// RememberGitLabProject records a project a connection delivered for, so the
// picker can offer it. The first project also names the connection after
// its namespace, which is the closest thing GitLab gives to an account.
func (s *Store) RememberGitLabProject(
	ctx context.Context, installationID int64, project GitLabProject,
) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO gitlab_projects (installation_id, project_id, path, web_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (installation_id, project_id) DO UPDATE
		SET path = EXCLUDED.path, web_url = EXCLUDED.web_url, seen_at = now()`,
		installationID, project.ID, project.Path, project.WebURL); err != nil {
		return fmt.Errorf("remember gitlab project: %w", err)
	}

	namespace := project.Path
	if i := strings.LastIndex(namespace, "/"); i > 0 {
		namespace = namespace[:i]
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE installations SET account_login = $2
		WHERE id = $1 AND account_login = ''`,
		installationID, namespace); err != nil {
		return fmt.Errorf("name gitlab connection: %w", err)
	}
	return nil
}

// GitLabProjects lists the projects a connection has delivered for, as seen
// by its owner.
func (s *Store) GitLabProjects(
	ctx context.Context, installationID, userID int64,
) ([]GitLabProject, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.project_id, p.path, p.web_url
		FROM gitlab_projects p
		JOIN installations ins ON ins.id = p.installation_id
		WHERE p.installation_id = $1 AND ins.user_id = $2
		ORDER BY p.path`, installationID, userID)
	if err != nil {
		return nil, fmt.Errorf("query gitlab projects: %w", err)
	}
	defer rows.Close()

	var out []GitLabProject
	for rows.Next() {
		var p GitLabProject
		if err := rows.Scan(&p.ID, &p.Path, &p.WebURL); err != nil {
			return nil, fmt.Errorf("scan gitlab project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteGitLabConnection removes a connection its owner no longer wants,
// together with its integrations. Unlike a GitHub installation there is no
// uninstall webhook to do it, so the owner does it from the bot.
func (s *Store) DeleteGitLabConnection(ctx context.Context, installationID, userID int64) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM installations
		WHERE id = $1 AND user_id = $2 AND provider = 'gitlab'`,
		installationID, userID); err != nil {
		return fmt.Errorf("delete gitlab connection: %w", err)
	}
	return nil
}

// ProviderForIntegration says which forge an integration's events come from,
// which decides the event kinds its settings screen offers.
func (s *Store) ProviderForIntegration(ctx context.Context, integrationID int64) (string, error) {
	var provider string
	err := s.pool.QueryRow(ctx, `
		SELECT ins.provider
		FROM integrations i
		JOIN installations ins ON ins.id = i.installation_id
		WHERE i.id = $1`, integrationID).Scan(&provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("integration %d not found", integrationID)
	}
	if err != nil {
		return "", fmt.Errorf("resolve integration provider: %w", err)
	}
	return provider, nil
}
