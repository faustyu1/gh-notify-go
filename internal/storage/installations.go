package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// ErrInstallationOwned reports a claim on an installation that already
// belongs to somebody else.
var ErrInstallationOwned = errors.New("installation already belongs to another user")

// ClaimInstallationOwner is the only way ownership is assigned. It takes an
// installation that has no owner yet, or reconfirms the one it already has —
// it never transfers. Transfers would be an account takeover: installation
// ids are neither secret nor unguessable, and nothing in the setup redirect
// proves who is on the GitHub side of it. Reassignment goes through
// uninstalling the App, which deletes the row.
func (s *Store) ClaimInstallationOwner(
	ctx context.Context, githubInstallationID int64, login, accountType string, userID int64,
) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO installations
			(github_installation_id, account_login, account_type, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (github_installation_id) DO UPDATE
		SET account_login = EXCLUDED.account_login,
		    account_type  = EXCLUDED.account_type,
		    user_id       = EXCLUDED.user_id,
		    suspended_at  = NULL
		WHERE installations.user_id IS NULL
		   OR installations.user_id = EXCLUDED.user_id`,
		githubInstallationID, login, accountType, userID)
	if err != nil {
		return fmt.Errorf("claim installation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInstallationOwned
	}
	return nil
}

// NewInstallState mints the single-use token that the install link carries as
// GitHub's `state`. The token is what proves the redirect belongs to this
// user; a user id would only be a claim.
func (s *Store) NewInstallState(
	ctx context.Context, userID int64, ttl time.Duration,
) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate install state: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO install_states (token, user_id, expires_at)
		VALUES ($1, $2, now() + $3::interval)`,
		token, userID, ttl.String()); err != nil {
		return "", fmt.Errorf("store install state: %w", err)
	}
	return token, nil
}

// TakeInstallState spends a token: it returns the user it was minted for and
// deletes it, so a leaked redirect URL cannot be replayed. Expired tokens are
// treated as unknown, and every visit clears whatever else has expired.
func (s *Store) TakeInstallState(ctx context.Context, token string) (int64, error) {
	var userID int64
	err := s.pool.QueryRow(ctx, `
		DELETE FROM install_states
		WHERE token = $1 AND expires_at > now()
		RETURNING user_id`, token).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("install state is unknown or expired")
	}
	if err != nil {
		return 0, fmt.Errorf("take install state: %w", err)
	}

	if _, err := s.pool.Exec(ctx,
		`DELETE FROM install_states WHERE expires_at <= now()`); err != nil {
		return 0, fmt.Errorf("purge install states: %w", err)
	}
	return userID, nil
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
