package janitor_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/janitor"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// fixture creates the rows every sweepable table hangs off and returns the
// pool plus the chat and integration ids an outbox row needs.
func fixture(t *testing.T) (*pgxpool.Pool, int64, int64, int64) {
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

	return pool, userID, chatID, integrationID
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func TestRunOnceKeepsRowsInsideRetention(t *testing.T) {
	ctx := context.Background()
	pool, userID, chatID, integrationID := fixture(t)

	// One spent row per sweep, each young enough to survive, plus a pending
	// outbox row: pending is not spent at any age and must never be touched.
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (chat_id, integration_id, event_kind, payload, status, sent_at)
		VALUES ($1, $2, 'push', '{}'::jsonb, 'sent', now() - interval '5 minutes'),
		       ($1, $2, 'push', '{}'::jsonb, 'failed', now()),
		       ($1, $2, 'push', '{}'::jsonb, 'pending', NULL)`, chatID, integrationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO ui_actions (key, user_id, screen, created_at)
		VALUES ('k', $1, 'home', now() - interval '1 hour')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, chat_id, action, created_at)
		VALUES ($1, $2, 'integration.create', now() - interval '1 day')`, userID, chatID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO gh_deliveries (delivery_id, received_at) VALUES ('d', now())`)
	require.NoError(t, err)

	require.NoError(t, janitor.New(pool, janitor.DefaultRetention(), time.Now).RunOnce(ctx))

	require.Equal(t, 3, count(t, pool, `SELECT count(*) FROM outbox`))
	require.Equal(t, 1, count(t, pool, `SELECT count(*) FROM ui_actions`))
	require.Equal(t, 1, count(t, pool, `SELECT count(*) FROM audit_log`))
	require.Equal(t, 1, count(t, pool, `SELECT count(*) FROM gh_deliveries`))
}

func TestRunOnceRemovesSpentRowsPastRetention(t *testing.T) {
	ctx := context.Background()
	pool, userID, chatID, integrationID := fixture(t)

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (chat_id, integration_id, event_kind, payload,
		                    status, sent_at, created_at)
		VALUES ($1, $2, 'push', '{}'::jsonb, 'sent',
		        now() - interval '2 hours', now() - interval '2 hours'),
		       ($1, $2, 'push', '{}'::jsonb, 'failed',
		        NULL, now() - interval '30 days'),
		       ($1, $2, 'push', '{}'::jsonb, 'pending',
		        NULL, now() - interval '30 days')`, chatID, integrationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO ui_actions (key, user_id, screen, created_at)
		VALUES ('old', $1, 'home', now() - interval '30 days')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, chat_id, action, created_at)
		VALUES ($1, $2, 'integration.create', now() - interval '200 days')`, userID, chatID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO gh_deliveries (delivery_id, received_at)
		VALUES ('old', now() - interval '30 days')`)
	require.NoError(t, err)

	require.NoError(t, janitor.New(pool, janitor.DefaultRetention(), time.Now).RunOnce(ctx))

	// The pending row is the survivor: a queued notification has an age but no
	// expiry, and sweeping it would silently drop a delivery.
	require.Equal(t, 1, count(t, pool, `SELECT count(*) FROM outbox`))
	require.Equal(t, 1,
		count(t, pool, `SELECT count(*) FROM outbox WHERE status = 'pending'`))
	require.Zero(t, count(t, pool, `SELECT count(*) FROM ui_actions`))
	require.Zero(t, count(t, pool, `SELECT count(*) FROM audit_log`))
	require.Zero(t, count(t, pool, `SELECT count(*) FROM gh_deliveries`))
}

func TestRunOnceBatchesLargeBacklogs(t *testing.T) {
	ctx := context.Background()
	pool, _, chatID, integrationID := fixture(t)

	// More rows than one batch holds, so the loop has to come back for the
	// rest instead of stopping at the LIMIT.
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (chat_id, integration_id, event_kind, payload, status, sent_at)
		SELECT $1, $2, 'push', '{}'::jsonb, 'sent', now() - interval '2 hours'
		FROM generate_series(1, 5200)`, chatID, integrationID)
	require.NoError(t, err)

	require.NoError(t, janitor.New(pool, janitor.DefaultRetention(), time.Now).RunOnce(ctx))
	require.Zero(t, count(t, pool, `SELECT count(*) FROM outbox`))
}

func TestRunOnceSkipsDisabledSweeps(t *testing.T) {
	ctx := context.Background()
	pool, _, chatID, integrationID := fixture(t)

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (chat_id, integration_id, event_kind, payload, status, sent_at)
		VALUES ($1, $2, 'push', '{}'::jsonb, 'sent', now() - interval '400 days')`,
		chatID, integrationID)
	require.NoError(t, err)

	// A zero retention means "keep", not "delete everything": an operator who
	// leaves a field unset must not lose the table.
	require.NoError(t, janitor.New(pool, janitor.Retention{}, time.Now).RunOnce(ctx))
	require.Equal(t, 1, count(t, pool, `SELECT count(*) FROM outbox`))
}
