package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A "label" rule must see the labels an issue or pull request carries in its
// nested list, not just the top-level label field of a "labeled" event.
func TestLabelFilterMatchesPullRequestLabels(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, 'label', 'skip*')`,
		integrationID)
	require.NoError(t, err)

	body := `{"action":"opened","number":7,
		"repository":{"id":42,"full_name":"acme/app"},
		"installation":{"id":7},
		"pull_request":{"html_url":"https://github.com/acme/app/pull/7",
			"head":{"ref":"feature"},"labels":[{"name":"skip-ci"},{"name":"docs"}]},
		"sender":{"login":"octocat","html_url":"https://github.com/octocat"}}`
	result, err := ingest.Handle(ctx, envelope("pull_request", "opened", "l-1", body))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped, "a matching label must suppress the event")
	require.Zero(t, result.Enqueued)
}

// "*" must reach across slashes: branch names are full paths.
func TestGlobAsteriskCrossesSlash(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, 'branch', 'renovate*')`,
		integrationID)
	require.NoError(t, err)

	body := `{"ref":"refs/heads/renovate/deps",
		"repository":{"id":42,"full_name":"acme/app"},
		"installation":{"id":7},
		"sender":{"login":"renovate","html_url":"https://github.com/renovate"},
		"commits":[]}`
	result, err := ingest.Handle(ctx, envelope("push", "", "g-1", body))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped, "renovate* must match renovate/deps")
	require.Zero(t, result.Enqueued)
}
