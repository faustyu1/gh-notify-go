package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

type tokenCache struct{ store *Store }

// TokenCache returns the encrypted installation-token cache backed by the
// installations table.
func (s *Store) TokenCache() ghapp.TokenCache { return tokenCache{store: s} }

func (c tokenCache) Get(ctx context.Context, installationID int64) (string, time.Time, error) {
	var (
		ciphertext []byte
		expiresAt  *time.Time
	)
	err := c.store.pool.QueryRow(ctx, `
		SELECT token_ciphertext, token_expires_at FROM installations
		WHERE github_installation_id = $1`, installationID).Scan(&ciphertext, &expiresAt)

	if errors.Is(err, pgx.ErrNoRows) || len(ciphertext) == 0 || expiresAt == nil {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token cache: %w", err)
	}

	plaintext, err := c.store.box.Open(ciphertext)
	if err != nil {
		// A key rotation invalidates old ciphertext. Treat it as a cache
		// miss rather than an outage: the token is re-minted.
		return "", time.Time{}, nil
	}
	return string(plaintext), *expiresAt, nil
}

func (c tokenCache) Put(
	ctx context.Context, installationID int64, token string, expiresAt time.Time,
) error {
	ciphertext, err := c.store.box.Seal([]byte(token))
	if err != nil {
		return fmt.Errorf("seal token: %w", err)
	}
	_, err = c.store.pool.Exec(ctx, `
		UPDATE installations
		SET token_ciphertext = $2, token_expires_at = $3
		WHERE github_installation_id = $1`, installationID, ciphertext, expiresAt)
	if err != nil {
		return fmt.Errorf("write token cache: %w", err)
	}
	return nil
}
