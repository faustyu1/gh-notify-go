package outbox_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// fixture creates one user, chat, installation and integration and returns
// the pool plus the integration and chat ids every outbox row needs.
func fixture(t *testing.T) (*pgxpool.Pool, int64, int64) {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID, chatID, installID, integrationID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (555) RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO chats (telegram_chat_id, title, kind)
		 VALUES (-100, 'Team', 'supergroup') RETURNING id`).Scan(&chatID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO installations (github_installation_id, account_login, account_type, user_id)
		 VALUES (7, 'acme', 'Organization', $1) RETURNING id`, userID).Scan(&installID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO integrations
		   (chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		 VALUES ($1, $2, 42, 'acme/app', $3) RETURNING id`,
		chatID, installID, userID).Scan(&integrationID))

	return pool, chatID, integrationID
}

func TestMarkDeliveredIsFreshOnlyOnce(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	fresh, err := queue.MarkDelivered(ctx, "delivery-1")
	require.NoError(t, err)
	require.True(t, fresh)

	// GitHub retries the same delivery: the second call must report it as
	// already seen so the handler returns 200 without fanning out again.
	fresh, err = queue.MarkDelivered(ctx, "delivery-1")
	require.NoError(t, err)
	require.False(t, fresh)
}

func TestEnqueueStoresPayloadAndSchedule(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID:        chatID,
		IntegrationID: integrationID,
		Kind:          "push",
		Payload:       json.RawMessage(`{"ref":"refs/heads/main"}`),
		Delay:         90 * time.Second,
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	var (
		status      string
		scheduledAt time.Time
		payload     []byte
		groupKey    *string
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, scheduled_at, payload, group_key FROM outbox WHERE id = $1`, id).
		Scan(&status, &scheduledAt, &payload, &groupKey))

	require.Equal(t, "pending", status)
	require.Equal(t, now.Add(90*time.Second), scheduledAt.UTC())
	require.JSONEq(t, `{"ref":"refs/heads/main"}`, string(payload))
	require.Nil(t, groupKey, "an empty GroupKey must be stored as NULL")
}

func TestEnqueueWithoutDelayIsImmediate(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "issues", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	var scheduledAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT scheduled_at FROM outbox WHERE id = $1`, id).Scan(&scheduledAt))
	require.Equal(t, now, scheduledAt.UTC())
}

func TestEnqueueStoresGroupKey(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "star", Payload: json.RawMessage(`{}`),
		GroupKey: "star:1:octocat",
	})
	require.NoError(t, err)

	var groupKey string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT group_key FROM outbox WHERE id = $1`, id).Scan(&groupKey))
	require.Equal(t, "star:1:octocat", groupKey)
}

func TestPruneDeliveriesRemovesOldRowsOnly(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	_, err := pool.Exec(ctx,
		`INSERT INTO gh_deliveries (delivery_id, received_at)
		 VALUES ('old', now() - interval '10 days'), ('new', now())`)
	require.NoError(t, err)

	removed, err := queue.PruneDeliveries(ctx, 7*24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)

	var remaining string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT delivery_id FROM gh_deliveries`).Scan(&remaining))
	require.Equal(t, "new", remaining)
}
