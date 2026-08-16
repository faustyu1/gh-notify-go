package storage_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func TestMigrationsCreateExpectedTables(t *testing.T) {
	ctx := context.Background()
	url := testhelper.StartPostgres(t)

	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	want := []string{
		"users", "installations", "chats", "chat_managers", "integrations",
		"event_settings", "filters", "outbox", "star_actors",
		"gh_deliveries", "ui_actions", "ui_nav", "audit_log",
	}
	for _, table := range want {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = $1)`, table,
		).Scan(&exists)
		require.NoError(t, err)
		require.Truef(t, exists, "table %q should exist", table)
	}
}

func TestIntegrationsRejectDuplicateRepoInChat(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID, chatID, installID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (1) RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO chats (telegram_chat_id, title, kind) VALUES (-100, 't', 'supergroup')
		 RETURNING id`).Scan(&chatID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO installations (github_installation_id, account_login, account_type, user_id)
		 VALUES (5, 'acme', 'Organization', $1) RETURNING id`, userID).Scan(&installID))

	insert := `INSERT INTO integrations
		(chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		VALUES ($1, $2, 99, 'acme/app', $3)`
	_, err = pool.Exec(ctx, insert, chatID, installID, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, insert, chatID, installID, userID)
	require.ErrorContains(t, err, "duplicate key")
}
