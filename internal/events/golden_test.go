package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

// -update rewrites the golden files. Review the diff before committing:
// a golden test only protects you if the expected output is read by a human
// at least once.
var update = flag.Bool("update", false, "rewrite golden files")

// Goldens are rendered in the default locale; the ru wording is covered by
// the locale parity tests in internal/i18n.
var goldenLoc = i18n.MustNewBundle().Localizer(i18n.Default)

// assertGolden renders testdata/<name>.json and compares it to
// testdata/<name>.golden.html.
func assertGolden(t *testing.T, kind events.Kind, name string) {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	got, err := events.Render(kind, goldenLoc, json.RawMessage(payload))
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

func TestIssueCommentGolden(t *testing.T) {
	assertGolden(t, "issue_comment", "issue_comment")
}

func TestPullRequestReviewGolden(t *testing.T) {
	assertGolden(t, "pull_request_review", "pull_request_review")
}

func TestReleaseGolden(t *testing.T) {
	assertGolden(t, "release", "release")
}

func TestForkGolden(t *testing.T) {
	assertGolden(t, "fork", "fork")
}

func TestStarGolden(t *testing.T) {
	assertGolden(t, "star", "star")
}

func TestCreateGolden(t *testing.T) {
	assertGolden(t, "create", "create")
}

func TestDeleteGolden(t *testing.T) {
	assertGolden(t, "delete", "delete")
}

func TestWorkflowRunGolden(t *testing.T) {
	assertGolden(t, "workflow_run", "workflow_run")
}

func TestCommitCommentGolden(t *testing.T) {
	assertGolden(t, "commit_comment", "commit_comment")
}

func TestPRReviewCommentGolden(t *testing.T) {
	assertGolden(t, "pull_request_review_comment", "pull_request_review_comment")
}

func TestGollumGolden(t *testing.T) {
	assertGolden(t, "gollum", "gollum")
}

func TestMemberGolden(t *testing.T) {
	assertGolden(t, "member", "member")
}

func TestPublicGolden(t *testing.T) {
	assertGolden(t, "public", "public")
}

func TestDeploymentGolden(t *testing.T) {
	assertGolden(t, "deployment", "deployment")
}

func TestDeploymentStatusGolden(t *testing.T) {
	assertGolden(t, "deployment_status", "deployment_status")
}

func TestCheckSuiteGolden(t *testing.T) {
	assertGolden(t, "check_suite", "check_suite")
}

func TestDigestGolden(t *testing.T) {
	assertGolden(t, "digest", "digest")
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
