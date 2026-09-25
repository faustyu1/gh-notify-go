package gitlab_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/gitlab"
)

func TestParseEnvelopeMapsKindAndProject(t *testing.T) {
	header := http.Header{}
	header.Set("X-Gitlab-Event-UUID", "abc-123")

	env, err := gitlab.ParseEnvelope(header, []byte(`{
		"object_kind":"merge_request",
		"project":{"id":15,"path_with_namespace":"mike/diaspora",
			"web_url":"https://gitlab.example.com/mike/diaspora"},
		"object_attributes":{"action":"merge"}}`))
	require.NoError(t, err)
	require.Equal(t, gitlab.KindMergeRequest, env.Kind)
	require.Equal(t, "merge", env.Action)
	require.Equal(t, int64(15), env.ProjectID)
	require.Equal(t, "mike/diaspora", env.ProjectPath)
	require.Equal(t, "https://gitlab.example.com/mike/diaspora", env.ProjectURL)
	require.Equal(t, "gitlab:abc-123", env.DeliveryID)
}

func TestParseEnvelopeActionPerKind(t *testing.T) {
	for _, tc := range []struct {
		body, kind, action string
	}{
		{`{"object_kind":"pipeline","object_attributes":{"status":"failed"}`, gitlab.KindPipeline, "failed"},
		{`{"object_kind":"deployment","status":"success"`, gitlab.KindDeployment, "success"},
		{`{"object_kind":"release","action":"create"`, gitlab.KindRelease, "create"},
		{`{"object_kind":"push","project_id":15`, gitlab.KindPush, ""},
	} {
		env, err := gitlab.ParseEnvelope(http.Header{}, []byte(tc.body+
			`,"project":{"id":15,"path_with_namespace":"a/b"}}`))
		require.NoError(t, err)
		require.Equal(t, tc.kind, env.Kind)
		require.Equal(t, tc.action, env.Action)
	}
}

// A kind the bot does not deliver still parses: the project it names is
// still worth remembering.
func TestParseEnvelopeUnknownKindHasNoKind(t *testing.T) {
	env, err := gitlab.ParseEnvelope(http.Header{}, []byte(`{"object_kind":"build",
		"project_id":3,"project":{"id":3,"path_with_namespace":"a/b"}}`))
	require.NoError(t, err)
	require.Empty(t, env.Kind)
	require.Equal(t, int64(3), env.ProjectID)
}

func TestParseEnvelopeRequiresProject(t *testing.T) {
	_, err := gitlab.ParseEnvelope(http.Header{}, []byte(`{"object_kind":"push"}`))
	require.Error(t, err)
}

func TestDeliveryIDFallsBackToBodyHash(t *testing.T) {
	body := []byte(`{"object_kind":"push","project":{"id":1,"path_with_namespace":"a/b"}}`)
	first, err := gitlab.ParseEnvelope(http.Header{}, body)
	require.NoError(t, err)
	second, err := gitlab.ParseEnvelope(http.Header{}, body)
	require.NoError(t, err)
	require.Equal(t, first.DeliveryID, second.DeliveryID)
	require.Contains(t, first.DeliveryID, "gitlab:sha256:")
}
