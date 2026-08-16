package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
)

// -update rewrites the golden files. Review the diff before committing:
// a golden test only protects you if the expected output is read by a human
// at least once.
var update = flag.Bool("update", false, "rewrite golden files")

// assertGolden renders testdata/<name>.json and compares it to
// testdata/<name>.golden.html.
func assertGolden(t *testing.T, kind events.Kind, name string) {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	got, err := events.Render(kind, json.RawMessage(payload))
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", name+".golden.html")
	if *update {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

func TestPushGolden(t *testing.T) {
	assertGolden(t, "push", "push")
}

func TestPullRequestOpenedGolden(t *testing.T) {
	assertGolden(t, "pull_request", "pull_request")
}

func TestPullRequestMergedGolden(t *testing.T) {
	assertGolden(t, "pull_request", "pull_request_merged")
}

func TestIssuesGolden(t *testing.T) {
	assertGolden(t, "issues", "issues")
}

func TestPullRequestActionFilter(t *testing.T) {
	require.True(t, events.Wanted("pull_request", "opened"))
	require.True(t, events.Wanted("pull_request", "ready_for_review"))
	// Label churn is the single noisiest PR action; it must not be sent.
	require.False(t, events.Wanted("pull_request", "labeled"))
	require.False(t, events.Wanted("pull_request", "synchronize"))
}

func TestIssuesActionFilter(t *testing.T) {
	require.True(t, events.Wanted("issues", "opened"))
	require.False(t, events.Wanted("issues", "labeled"))
}
