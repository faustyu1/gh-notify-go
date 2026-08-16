// Package storage owns every SQL statement in the application. Nothing above
// this package writes SQL.
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faustyu/gh-notify-go/internal/secret"
)

type Store struct {
	pool *pgxpool.Pool
	box  *secret.Box
}

func New(ctx context.Context, dbURL string, box *secret.Box) (*Store, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool, box: box}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool is exposed for tests and for the outbox worker, which needs explicit
// transaction control that the typed methods do not provide.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
