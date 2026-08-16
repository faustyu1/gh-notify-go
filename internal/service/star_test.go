package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

func starEnvelope(action, login, delivery string) ghapp.Envelope {
	body := `{
		"action": "` + action + `",
		"repository": {"id": 42, "full_name": "acme/app", "stargazers_count": 341,
			"html_url": "https://github.com/acme/app"},
		"sender": {"login": "` + login + `", "html_url": "https://github.com/` + login + `"},
		"installation": {"id": 7}
	}`
	env, _ := ghapp.ParseEnvelope("star", delivery, []byte(body))
	env.Action = action
	return env
}

func TestStarNotifiesOncePerActorForever(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	result, err := ingest.Handle(ctx, starEnvelope("created", "octocat", "s-1"))
	require.NoError(t, err)
	require.Equal(t, 1, result.Enqueued)

	// Same actor stars again: silent forever.
	result, err = ingest.Handle(ctx, starEnvelope("created", "octocat", "s-2"))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)
	require.Equal(t, 1, result.Skipped)

	// The whole toggle dance — unstar, star — is equally silent.
	result, err = ingest.Handle(ctx, starEnvelope("deleted", "octocat", "s-3"))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)
	result, err = ingest.Handle(ctx, starEnvelope("created", "octocat", "s-4"))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestStarIsDeliveredImmediately(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, starEnvelope("created", "defunkt", "s-1"))
	require.NoError(t, err)

	// No debounce window anymore: under the old behaviour this row would sit
	// at now+60s. A one-minute epsilon covers app/DB clock drift.
	var ready int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star'
		  AND scheduled_at <= now() + interval '1 minute'`).
		Scan(&ready))
	require.Equal(t, 1, ready)

	var payload []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT payload FROM outbox WHERE event_kind = 'star' LIMIT 1`).Scan(&payload))
	var parsed struct {
		Actors []string `json:"actors"`
	}
	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.Equal(t, []string{"defunkt"}, parsed.Actors)
}

func TestStarDifferentActorsEachGetOne(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, starEnvelope("created", "octocat", "s-1"))
	require.NoError(t, err)
	_, err = ingest.Handle(ctx, starEnvelope("created", "defunkt", "s-2"))
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star'`).Scan(&count))
	require.Equal(t, 2, count, "each first-time actor gets their own message")
}

func TestFiltersSkipMatchingEvents(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, 'author', 'dependabot*')`,
		integrationID)
	require.NoError(t, err)

	body := `{"ref":"refs/heads/main",
		"repository":{"id":42,"full_name":"acme/app"},
		"installation":{"id":7},
		"sender":{"login":"Dependabot","html_url":"https://github.com/dependabot"},
		"commits":[]}`
	result, err := ingest.Handle(ctx, envelope("push", "", "f-1", body))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Skipped, "a matching author filter must suppress the event")
	require.Zero(t, result.Enqueued)

	// A different author is unaffected.
	body = `{"ref":"refs/heads/main",
		"repository":{"id":42,"full_name":"acme/app"},
		"installation":{"id":7},
		"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
		"commits":[]}`
	result, err = ingest.Handle(ctx, envelope("push", "", "f-2", body))
	require.NoError(t, err)
	require.Equal(t, 1, result.Enqueued)
}

func TestBranchFilterMatchesRef(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, 'branch', 'renovate/*')`,
		integrationID)
	require.NoError(t, err)

	body := `{"ref":"refs/heads/renovate/deps",
		"repository":{"id":42,"full_name":"acme/app"},
		"installation":{"id":7},
		"sender":{"login":"renovate","html_url":"https://github.com/renovate"},
		"commits":[]}`
	result, err := ingest.Handle(ctx, envelope("push", "", "f-3", body))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Enqueued)
}
