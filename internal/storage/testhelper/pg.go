// Package testhelper starts a throwaway Postgres for storage tests and
// applies the real migrations to it, so tests exercise the actual schema
// rather than a hand-maintained copy.
package testhelper

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/faustyu/gh-notify-go/internal/storage/migrations"
)

// StartPostgres boots a container, migrates it, and returns its URL. The
// container is torn down when the test finishes.
func StartPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("ghnotify"),
		tcpostgres.WithUsername("gh"),
		tcpostgres.WithPassword("gh"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = testcontainers.TerminateContainer(container)
	})

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	require.NoError(t, migrations.Up(ctx, url))
	return url
}
