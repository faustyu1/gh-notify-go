package ghapp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

// newClient wires a TokenSource whose cache already holds a valid token, so
// these tests exercise the REST calls rather than the minting path.
func newClient(t *testing.T, handler http.Handler) *ghapp.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	cache := &memCache{token: "ghs_valid", expires: now.Add(time.Hour)}
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(), cache,
		func() time.Time { return now })
	src.BaseURL = server.URL

	client := ghapp.NewClient(src, server.Client())
	client.BaseURL = server.URL
	return client
}

func TestListRepositoriesFollowsPagination(t *testing.T) {
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token ghs_valid", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
				{"id":3,"full_name":"acme/c","private":false,"description":""}]}`))
			return
		}
		w.Header().Set("Link",
			fmt.Sprintf(`<%s/installation/repositories?page=2>; rel="next"`, serverURL))
		_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
			{"id":1,"full_name":"acme/a","private":true,"description":"first"},
			{"id":2,"full_name":"acme/b","private":false,"description":""}]}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	serverURL = server.URL

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(),
		&memCache{token: "ghs_valid", expires: now.Add(time.Hour)},
		func() time.Time { return now })
	src.BaseURL = server.URL
	client := ghapp.NewClient(src, server.Client())
	client.BaseURL = server.URL

	repos, err := client.ListRepositories(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, repos, 3)
	require.Equal(t, "acme/a", repos[0].FullName)
	require.True(t, repos[0].Private)
	require.Equal(t, "acme/c", repos[2].FullName)
}

func TestCompareStatsReturnsTotals(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/acme/app/compare/aaa...bbb", r.URL.Path)
		_, _ = w.Write([]byte(`{"files":[
			{"additions":10,"deletions":2},
			{"additions":1,"deletions":0}]}`))
	}))

	stat, err := client.CompareStats(context.Background(), 7, "acme/app", "aaa", "bbb")
	require.NoError(t, err)
	require.Equal(t, 11, stat.Additions)
	require.Equal(t, 2, stat.Deletions)
	require.Equal(t, 2, stat.ChangedFiles)
}

func TestCompareStatsTreatsMissingCompareAsEmpty(t *testing.T) {
	// A force-push can leave a base commit GitHub no longer knows about.
	// That must degrade to "no stats", not fail the whole notification.
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	stat, err := client.CompareStats(context.Background(), 7, "acme/app", "aaa", "bbb")
	require.NoError(t, err)
	require.Equal(t, ghapp.DiffStat{}, stat)
}
