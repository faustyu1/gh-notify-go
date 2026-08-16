package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/service"
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

func TestStarDebounceCancelsOnUnstar(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, starEnvelope("created", "octocat", "s-1"))
	require.NoError(t, err)

	var pending int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star' AND status = 'pending'`).
		Scan(&pending))
	require.Equal(t, 1, pending)

	// The unstar arrives inside the window: the pending row must disappear
	// and nothing is ever sent.
	_, err = ingest.Handle(ctx, starEnvelope("deleted", "octocat", "s-2"))
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star'`).
		Scan(&pending))
	require.Zero(t, pending)
}

func TestStarCoalescesActorsIntoOneRow(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, starEnvelope("created", "octocat", "s-1"))
	require.NoError(t, err)
	_, err = ingest.Handle(ctx, starEnvelope("created", "defunkt", "s-2"))
	require.NoError(t, err)

	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'star'`).Scan(&rows))
	require.Equal(t, 1, rows, "two actors must share one coalesced row")

	var payload []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT payload FROM outbox WHERE event_kind = 'star' LIMIT 1`).Scan(&payload))

	var parsed struct {
		Actors []string `json:"actors"`
	}
	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.ElementsMatch(t, []string{"octocat", "defunkt"}, parsed.Actors)
}

func TestStarCooldownSilencesRepeatActor(t *testing.T) {
	ctx := context.Background()
	ingest, _, integrationID := newIngest(t)

	result, err := ingest.Handle(ctx, starEnvelope("created", "octocat", "s-1"))
	require.NoError(t, err)
	require.Equal(t, 1, result.Enqueued)

	// Same actor again inside the 24h cooldown: silently skipped.
	result, err = ingest.Handle(ctx, starEnvelope("created", "octocat", "s-2"))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Zero(t, result.Enqueued)

	_ = integrationID
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
	require.NotEqual(t, service.Result{}, result)
}
