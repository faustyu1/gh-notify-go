package service_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/gitlab"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// newGitLabIngest wires one GitLab connection with project 15 connected to a
// chat, and returns the connection id and the integration id.
func newGitLabIngest(t *testing.T) (*service.Ingest, *storage.Store, *pgxpool.Pool, int64, int64) {
	t.Helper()
	ctx := context.Background()

	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)
	store, err := storage.New(ctx, testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	userID, _, err := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, err)
	connection, err := store.CreateGitLabConnection(ctx, userID)
	require.NoError(t, err)
	integrationID, err := store.CreateIntegration(ctx, chatID, connection, 15, "mike/diaspora", userID)
	require.NoError(t, err)

	return service.NewIngest(store, outbox.NewQueue(store.Pool(), time.Now)),
		store, store.Pool(), connection, integrationID
}

func glEnvelope(t *testing.T, uuid, body string) gitlab.Envelope {
	t.Helper()
	header := http.Header{}
	if uuid != "" {
		header.Set("X-Gitlab-Event-UUID", uuid)
	}
	env, err := gitlab.ParseEnvelope(header, []byte(body))
	require.NoError(t, err)
	return env
}

const glMergeRequestBody = `{"object_kind":"merge_request",
	"user":{"username":"root"},
	"project":{"id":15,"path_with_namespace":"mike/diaspora",
		"web_url":"https://gitlab.example.com/mike/diaspora"},
	"object_attributes":{"iid":1,"action":"open","source_branch":"feature",
		"target_branch":"main"},
	"labels":[{"title":"skip-ci"}]}`

func TestHandleGitLabEnqueuesAndRemembersProject(t *testing.T) {
	ctx := context.Background()
	ingest, _, pool, connection, _ := newGitLabIngest(t)

	result, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "u-1", glMergeRequestBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Enqueued)

	var kind string
	require.NoError(t, pool.QueryRow(ctx, `SELECT event_kind FROM outbox`).Scan(&kind))
	require.Equal(t, gitlab.KindMergeRequest, kind)

	var path string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT path FROM gitlab_projects WHERE installation_id = $1`, connection).Scan(&path))
	require.Equal(t, "mike/diaspora", path)

	again, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "u-1", glMergeRequestBody))
	require.NoError(t, err)
	require.True(t, again.Duplicate)
}

// The webhook's "Test" button sends whatever kind the user picks; even one
// that is never delivered must make the project show up.
func TestHandleGitLabRemembersProjectOfUnwantedKind(t *testing.T) {
	ctx := context.Background()
	ingest, _, pool, connection, _ := newGitLabIngest(t)

	result, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "", `{"object_kind":"build",
		"project_id":99,"project":{"id":99,"path_with_namespace":"acme/other"}}`))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM gitlab_projects WHERE project_id = 99`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestHandleGitLabHonoursFilters(t *testing.T) {
	ctx := context.Background()
	ingest, _, pool, connection, integrationID := newGitLabIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO filters (integration_id, kind, pattern) VALUES ($1, 'label', 'skip*')`,
		integrationID)
	require.NoError(t, err)

	result, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "u-2", glMergeRequestBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Enqueued)
}

func TestHandleGitLabBranchAndAuthorFilters(t *testing.T) {
	ctx := context.Background()
	ingest, _, pool, connection, integrationID := newGitLabIngest(t)

	_, err := pool.Exec(ctx, `INSERT INTO filters (integration_id, kind, pattern)
		VALUES ($1, 'branch', 'renovate/*'), ($1, 'author', 'bot')`, integrationID)
	require.NoError(t, err)

	push := `{"object_kind":"push","ref":"refs/heads/renovate/deps","user_username":"alice",
		"project_id":15,"project":{"id":15,"path_with_namespace":"mike/diaspora"}}`
	result, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "p-1", push))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped, "branch rule must match a push ref")

	note := `{"object_kind":"note","user":{"username":"bot"},
		"project":{"id":15,"path_with_namespace":"mike/diaspora"},
		"object_attributes":{"noteable_type":"Issue"}}`
	result, err = ingest.HandleGitLab(ctx, connection, glEnvelope(t, "n-1", note))
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped, "author rule must match a note's user")
}

// One GitLab event reaches every webhook with the same UUID; two connections
// covering the project must both deliver.
func TestHandleGitLabDedupIsPerConnection(t *testing.T) {
	ctx := context.Background()
	ingest, store, _, connection, _ := newGitLabIngest(t)

	userID, _, err := store.UpsertUser(ctx, 556, "en")
	require.NoError(t, err)
	second, err := store.CreateGitLabConnection(ctx, userID)
	require.NoError(t, err)

	first, err := ingest.HandleGitLab(ctx, connection, glEnvelope(t, "same", glMergeRequestBody))
	require.NoError(t, err)
	require.False(t, first.Duplicate)
	other, err := ingest.HandleGitLab(ctx, second, glEnvelope(t, "same", glMergeRequestBody))
	require.NoError(t, err)
	require.False(t, other.Duplicate)
}
