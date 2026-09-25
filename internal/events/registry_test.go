package events_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

func TestRenderUnknownKindIsAnError(t *testing.T) {
	loc := i18n.MustNewBundle().Localizer(i18n.Default)
	_, err := events.Render("no_such_event", loc, json.RawMessage(`{}`))
	require.ErrorIs(t, err, events.ErrUnknownKind)
}

func TestWantedHonoursActionFilter(t *testing.T) {
	// push registers with an empty filter: every delivery is wanted.
	require.True(t, events.Wanted("push", ""))
	require.True(t, events.Wanted("push", "anything"))
}

func TestWantedRejectsUnknownKind(t *testing.T) {
	require.False(t, events.Wanted("no_such_event", "opened"))
}

func TestKindsIncludesRegisteredEvents(t *testing.T) {
	require.Contains(t, events.Kinds(), events.Kind("push"))
}

func TestKindsForSeparatesProviders(t *testing.T) {
	github := events.KindsFor("github")
	gitlab := events.KindsFor("gitlab")

	require.Contains(t, github, events.Kind("push"))
	require.NotContains(t, github, events.Kind("gl_push"))
	require.Contains(t, gitlab, events.Kind("gl_push"))
	require.Contains(t, gitlab, events.Kind("gl_merge_request"))
	require.NotContains(t, gitlab, events.Kind("push"))

	// Gitea and its forks share one payload format and so one set of kinds.
	gitea := events.KindsFor("gitea")
	require.Contains(t, gitea, events.Kind("gt_pull_request"))
	require.NotContains(t, gitea, events.Kind("pull_request"))
	require.Equal(t, gitea, events.KindsFor("forgejo"))
	require.Equal(t, gitea, events.KindsFor("gitverse"))

	require.Len(t, events.Kinds(), len(github)+len(gitlab)+len(gitea))
}

func TestLabelDropsProviderPrefix(t *testing.T) {
	require.Equal(t, "merge_request", events.Label("gl_merge_request"))
	require.Equal(t, "pull_request", events.Label("gt_pull_request"))
	require.Equal(t, "push", events.Label("push"))
}

func TestGitLabMergeRequestUpdatesAreNotWanted(t *testing.T) {
	require.True(t, events.Wanted("gl_merge_request", "open"))
	require.True(t, events.Wanted("gl_merge_request", "merge"))
	// "update" fires on every push to the source branch.
	require.False(t, events.Wanted("gl_merge_request", "update"))
	require.False(t, events.Wanted("gl_pipeline", "running"))
}
