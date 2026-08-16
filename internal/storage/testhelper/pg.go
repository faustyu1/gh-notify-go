// Package testhelper gives storage tests a throwaway Postgres. One container
// is booted per test binary; every test then gets its own freshly migrated
// database inside it, so the suite starts 1 container instead of one per
// test and still runs each test against an isolated schema.
package testhelper

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/faustyu/gh-notify-go/internal/storage/migrations"
)

var (
	startOnce sync.Once
	adminURL  string
	startErr  error
	dbCounter atomic.Uint64
)

// bootContainer picks the shared Postgres server once per test binary. When
// TEST_DATABASE_URL points at an already-running server (started by
// `make test`), it is used directly; otherwise a testcontainers instance is
// booted and left to the reaper for teardown.
func bootContainer() {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		adminURL = v
		return
	}

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
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	adminURL, startErr = container.ConnectionString(ctx, "sslmode=disable")
}

// StartPostgres returns the URL of an empty, migrated database. The database
// is dropped when the test finishes; the container is shared by the whole
// test binary.
func StartPostgres(t *testing.T) string {
	t.Helper()
	startOnce.Do(bootContainer)
	require.NoError(t, startErr)

	ctx := context.Background()
	dbName := fmt.Sprintf("t_%d_%d", os.Getpid(), dbCounter.Add(1))

	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	_, err = admin.Exec(ctx, "CREATE DATABASE "+dbName)
	require.NoError(t, err)
	t.Cleanup(func() {
		// FORCE kicks out connections a test forgot to close; the schema
		// goes away with the database either way.
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
	})

	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	u.Path = "/" + dbName
	testURL := u.String()

	require.NoError(t, migrations.Up(ctx, testURL))
	return testURL
}
