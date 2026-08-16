package storage

import (
	"context"
	"fmt"
)

// RegisterInstallation records an installation from the webhook, when the
// owning Telegram user is not yet known. An existing owner is never
// overwritten — ownership is claimed once, through the setup redirect.
func (s *Store) RegisterInstallation(
	ctx context.Context, githubInstallationID int64, login, accountType string,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO installations
			(github_installation_id, account_login, account_type, user_id)
		VALUES ($1, $2, $3, NULL)
		ON CONFLICT (github_installation_id) DO UPDATE
		SET account_login = EXCLUDED.account_login,
		    account_type  = EXCLUDED.account_type`,
		githubInstallationID, login, accountType)
	if err != nil {
		return fmt.Errorf("register installation: %w", err)
	}
	return nil
}

// SetInstallationSuspended flags or unflags a suspended installation; a
// suspended one cannot mint tokens and is shown as broken in the accounts
// screen rather than quietly failing on use.
func (s *Store) SetInstallationSuspended(
	ctx context.Context, githubInstallationID int64, suspended bool,
) error {
	var query string
	if suspended {
		query = `UPDATE installations SET suspended_at = now()
		         WHERE github_installation_id = $1`
	} else {
		query = `UPDATE installations SET suspended_at = NULL
		         WHERE github_installation_id = $1`
	}
	if _, err := s.pool.Exec(ctx, query, githubInstallationID); err != nil {
		return fmt.Errorf("set installation suspended: %w", err)
	}
	return nil
}

// DeleteInstallation removes an uninstalled App. Cascades to integrations:
// an installation GitHub no longer knows about must stop delivering, and
// leaving half-dead integrations would only produce errors.
func (s *Store) DeleteInstallation(ctx context.Context, githubInstallationID int64) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM installations WHERE github_installation_id = $1`,
		githubInstallationID); err != nil {
		return fmt.Errorf("delete installation: %w", err)
	}
	return nil
}
