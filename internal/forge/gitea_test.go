package forge_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/forge"
)

const giteaRepo = `"repository":{"id":7,"full_name":"mike/tea",
	"html_url":"https://tea.example.com/mike/tea"}`

func TestGiteaParseMapsEventHeader(t *testing.T) {
	for _, tc := range []struct {
		header, value, body, kind, action string
	}{
		{"X-Gitea-Event", "push", `{"ref":"refs/heads/main","commits":[]`, forge.KindGiteaPush, ""},
		{"X-Forgejo-Event", "pull_request", `{"action":"opened","pull_request":{}`, forge.KindGiteaPullRequest, "opened"},
		{"X-Gitea-Event", "issue_comment", `{"action":"created"`, forge.KindGiteaIssueComment, "created"},
		{"X-Gitea-Event", "pull_request_approved",
			`{"action":"reviewed","review":{"type":"pull_request_review_approved"}`,
			forge.KindGiteaPullRequestReview, "approved"},
		{"X-Gitea-Event", "pull_request_rejected",
			`{"action":"reviewed","review":{"type":"pull_request_review_rejected"}`,
			forge.KindGiteaPullRequestReview, "rejected"},
		{"X-Gitea-Event", "create", `{"ref":"v1","ref_type":"tag"`, forge.KindGiteaCreate, ""},
		{"X-Gitea-Event", "release", `{"action":"published"`, forge.KindGiteaRelease, "published"},
		{"X-Gitea-Event", "repository", `{"action":"created"`, "", "created"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			h := http.Header{}
			h.Set(tc.header, tc.value)
			h.Set("X-Gitea-Delivery", "d-1")
			env, err := forge.Gitea.Parse(h, []byte(tc.body+","+giteaRepo+"}"))
			require.NoError(t, err)
			require.Equal(t, tc.kind, env.Kind)
			require.Equal(t, tc.action, env.Action)
			require.Equal(t, int64(7), env.ProjectID)
			require.Equal(t, "mike/tea", env.ProjectPath)
			require.Equal(t, "https://tea.example.com/mike/tea", env.ProjectURL)
			require.Equal(t, "gitea:d-1", env.DeliveryID)
		})
	}
}

// GitVerse's header set is not documented, so a delivery without a known
// event header is read from its shape.
func TestGiteaParseGuessesEventWithoutHeader(t *testing.T) {
	for _, tc := range []struct{ body, kind string }{
		{`{"ref":"refs/heads/main","commits":[{"id":"a"}]`, forge.KindGiteaPush},
		{`{"ref":"feature","ref_type":"branch"`, forge.KindGiteaCreate},
		{`{"action":"opened","number":1,"pull_request":{"id":1}`, forge.KindGiteaPullRequest},
		{`{"action":"reviewed","pull_request":{"id":1},"review":{"type":"pull_request_review_comment"}`,
			forge.KindGiteaPullRequestReview},
		{`{"action":"created","issue":{"id":1},"comment":{"id":2}`, forge.KindGiteaIssueComment},
		{`{"action":"opened","issue":{"id":1}`, forge.KindGiteaIssues},
		{`{"action":"published","release":{"id":1}`, forge.KindGiteaRelease},
		{`{"action":"edited","page":"Home"`, forge.KindGiteaWiki},
	} {
		env, err := forge.GitVerse.Parse(http.Header{}, []byte(tc.body+","+giteaRepo+"}"))
		require.NoError(t, err)
		require.Equal(t, tc.kind, env.Kind, tc.body)
		require.Contains(t, env.DeliveryID, "gitverse:sha256:")
	}
}

func TestGiteaParseRequiresRepository(t *testing.T) {
	_, err := forge.Gitea.Parse(http.Header{}, []byte(`{"ref":"refs/heads/main"}`))
	require.Error(t, err)
}

func TestGiteaVerify(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	for _, tc := range []struct {
		header, value string
		want          bool
	}{
		{"X-Gitea-Signature", sig, true},
		{"X-Forgejo-Signature", sig, true},
		{"X-Hub-Signature-256", "sha256=" + sig, true},
		{"X-Gitea-Signature", "00" + sig[2:], false},
		{"X-Gitea-Signature", "not hex", false},
	} {
		h := http.Header{}
		h.Set(tc.header, tc.value)
		require.Equal(t, tc.want, forge.Forgejo.Verify("s3cret", h, body), tc.header+" "+tc.value)
	}
	require.False(t, forge.Gitea.Verify("s3cret", http.Header{}, body), "unsigned")
	require.False(t, forge.Gitea.Verify("", http.Header{"X-Gitea-Signature": {sig}}, body),
		"an empty secret never verifies")
}

func TestGitVerseTokenStripsScheme(t *testing.T) {
	for _, v := range []string{"tok", "Bearer tok", "token tok", "  Bearer   tok "} {
		h := http.Header{}
		h.Set("Authorization", v)
		require.Equal(t, "tok", forge.GitVerse.Token(h), v)
	}
}

func TestSubjectsFollowTheKindsProvider(t *testing.T) {
	gt := forge.ForKind(forge.KindGiteaPullRequest).Subjects(forge.KindGiteaPullRequest, []byte(`{
		"action":"opened","sender":{"login":"mike"},
		"pull_request":{"head":{"ref":"renovate/deps"},"labels":[{"name":"deps"}]}}`))
	require.Equal(t, forge.Subjects{Author: "mike", Branch: "renovate/deps",
		Labels: []string{"deps"}, Action: "opened"}, gt)

	push := forge.ForKind(forge.KindGiteaPush).Subjects(forge.KindGiteaPush, []byte(`{
		"ref":"refs/heads/main","pusher":{"login":"mike"}}`))
	require.Equal(t, "mike", push.Author)
	require.Equal(t, "main", push.Branch)

	review := forge.ForKind(forge.KindGiteaPullRequestReview).Subjects(forge.KindGiteaPullRequestReview,
		[]byte(`{"action":"reviewed","review":{"type":"pull_request_review_approved"}}`))
	require.Equal(t, "approved", review.Action)

	require.Equal(t, forge.GitLab, forge.ForKind(forge.KindGitLabPush))
	require.Equal(t, forge.GitHub, forge.ForKind("push"))
}

func TestWebhookPath(t *testing.T) {
	require.Equal(t, "/hook/gitlab", forge.WebhookPath(forge.GitLab, 5))
	require.Equal(t, "/hook/gitverse", forge.WebhookPath(forge.GitVerse, 5))
	require.Equal(t, "/hook/gitea/5", forge.WebhookPath(forge.Gitea, 5))
	require.Equal(t, "/hook/forgejo/5", forge.WebhookPath(forge.Forgejo, 5))
}
