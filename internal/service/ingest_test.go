package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	_ "github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func newIngest(t *testing.T) (*service.Ingest, *pgxpool.Pool, int64) {
	t.Helper()
	ctx := context.Background()

	url := testhelper.StartPostgres(t)
	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)

	store, err := storage.New(ctx, url, box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	userID, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)
	integrationID, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	queue := outbox.NewQueue(store.Pool(), time.Now)
	return service.NewIngest(store, queue, 60*time.Second, 24*time.Hour), store.Pool(), integrationID
}

func envelope(kind, action, delivery string, body string) ghapp.Envelope {
	env, _ := ghapp.ParseEnvelope(kind, delivery, []byte(body))
	env.Action = action
	return env
}

const pushBody = `{
	"ref":"refs/heads/main",
	"repository":{"id":42,"full_name":"acme/app"},
	"installation":{"id":7},
	"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
	"commits":[]
}`

func TestHandleEnqueuesForMatchingIntegration(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Enqueued)
	require.False(t, result.Duplicate)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'push'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestHandleIgnoresRepeatedDelivery(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.True(t, result.Duplicate)
	require.Zero(t, result.Enqueued)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestHandleSkipsDisabledEventKind(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO event_settings (integration_id, event_kind, enabled)
		 VALUES ($1, 'push', false)`, integrationID)
	require.NoError(t, err)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Enqueued)
}

func TestHandleSkipsUnwantedAction(t *testing.T) {
	ctx := context.Background()
	ingest, _, _ := newIngest(t)

	body := `{"action":"labeled","repository":{"id":42,"full_name":"acme/app"},
	          "installation":{"id":7},"pull_request":{}}`
	result, err := ingest.Handle(ctx, envelope("pull_request", "labeled", "d-2", body))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)
	require.Zero(t, result.Matched, "an unwanted action must not even query integrations")
}

func TestHandleWithNoIntegrationsIsQuiet(t *testing.T) {
	ctx := context.Background()
	ingest, _, _ := newIngest(t)

	body := `{"repository":{"id":999,"full_name":"other/repo"},
	          "installation":{"id":7},"commits":[]}`
	result, err := ingest.Handle(ctx, envelope("push", "", "d-3", body))
	require.NoError(t, err)
	require.Zero(t, result.Matched)
	require.Zero(t, result.Enqueued)
}

func TestHandleStoresFullPayload(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)

	var payload []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT payload FROM outbox`).Scan(&payload))

	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.Contains(t, parsed, "repository")
	require.Contains(t, parsed, "sender")
}
