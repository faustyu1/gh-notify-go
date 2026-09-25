package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/httpapi"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// fakeAuth knows tokens per provider and secrets per connection.
type fakeAuth struct {
	tokens  map[string]int64 // provider + ":" + token
	secrets map[int64]string
}

func (f fakeAuth) InstallationByToken(_ context.Context, provider, token string) (int64, error) {
	if id, ok := f.tokens[provider+":"+token]; ok {
		return id, nil
	}
	return 0, storage.ErrUnknownWebhookToken
}

func (f fakeAuth) WebhookSecret(_ context.Context, _ string, id int64) (string, error) {
	if s, ok := f.secrets[id]; ok {
		return s, nil
	}
	return "", storage.ErrUnknownWebhookToken
}

type fakeIngest struct {
	installation int64
	env          forge.Envelope
	calls        int
}

func (f *fakeIngest) HandleHook(
	_ context.Context, installationID int64, env forge.Envelope,
) (service.Result, error) {
	f.calls++
	f.installation, f.env = installationID, env
	return service.Result{}, nil
}

func serve(auth fakeAuth, ingest *fakeIngest, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	httpapi.MountHooks(mux, auth, ingest)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

const glPush = `{"object_kind":"push","project_id":15,
	"project":{"id":15,"path_with_namespace":"mike/diaspora"}}`

const gtPush = `{"ref":"refs/heads/main","commits":[{"id":"abc"}],
	"repository":{"id":7,"full_name":"mike/tea","html_url":"https://tea.example.com/mike/tea"}}`

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestGitLabWebhookRejectsUnknownToken(t *testing.T) {
	ingest := &fakeIngest{}
	req := httptest.NewRequest(http.MethodPost, "/gl/webhook", bytes.NewReader([]byte(glPush)))
	req.Header.Set("X-Gitlab-Token", "bad")
	rec := serve(fakeAuth{tokens: map[string]int64{"gitlab:good": 3}}, ingest, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Zero(t, ingest.calls)
}

// The old URL and the new one reach the same connection.
func TestGitLabWebhookRoutesToConnection(t *testing.T) {
	for _, path := range []string{"/gl/webhook", "/hook/gitlab"} {
		ingest := &fakeIngest{}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(glPush)))
		req.Header.Set("X-Gitlab-Token", "good")
		req.Header.Set("X-Gitlab-Event", "Push Hook")
		rec := serve(fakeAuth{tokens: map[string]int64{"gitlab:good": 3}}, ingest, req)

		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, int64(3), ingest.installation)
		require.Equal(t, forge.KindGitLabPush, ingest.env.Kind)
		require.Equal(t, int64(15), ingest.env.ProjectID)
	}
}

func TestHookRejectsNonPost(t *testing.T) {
	rec := serve(fakeAuth{}, &fakeIngest{}, httptest.NewRequest(http.MethodGet, "/gl/webhook", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestWebhookUnknownProviderIsNotFound(t *testing.T) {
	rec := serve(fakeAuth{}, &fakeIngest{},
		httptest.NewRequest(http.MethodPost, "/hook/sourceforge", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGiteaWebhookChecksSignature(t *testing.T) {
	auth := fakeAuth{secrets: map[int64]string{9: "s3cret"}}

	for _, tc := range []struct {
		name, path, header, sig string
		want                    int
	}{
		{"gitea", "/hook/gitea/9", "X-Gitea-Signature", sign("s3cret", gtPush), http.StatusOK},
		{"forgejo", "/hook/forgejo/9", "X-Forgejo-Signature", sign("s3cret", gtPush), http.StatusOK},
		{"wrong secret", "/hook/gitea/9", "X-Gitea-Signature", sign("other", gtPush), http.StatusUnauthorized},
		{"no signature", "/hook/gitea/9", "", "", http.StatusUnauthorized},
		{"unknown connection", "/hook/gitea/10", "X-Gitea-Signature", sign("s3cret", gtPush), http.StatusUnauthorized},
		{"no connection", "/hook/gitea", "X-Gitea-Signature", sign("s3cret", gtPush), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ingest := &fakeIngest{}
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader([]byte(gtPush)))
			req.Header.Set("X-Gitea-Event", "push")
			if tc.header != "" {
				req.Header.Set(tc.header, tc.sig)
			}
			rec := serve(auth, ingest, req)

			require.Equal(t, tc.want, rec.Code)
			if tc.want == http.StatusOK {
				require.Equal(t, int64(9), ingest.installation)
				require.Equal(t, forge.KindGiteaPush, ingest.env.Kind)
				require.Equal(t, int64(7), ingest.env.ProjectID)
			} else {
				require.Zero(t, ingest.calls)
			}
		})
	}
}

func TestGitVerseWebhookAuthenticatesByAuthorizationHeader(t *testing.T) {
	auth := fakeAuth{tokens: map[string]int64{"gitverse:tok": 4}}

	for _, value := range []string{"tok", "Bearer tok"} {
		ingest := &fakeIngest{}
		req := httptest.NewRequest(http.MethodPost, "/hook/gitverse", bytes.NewReader([]byte(gtPush)))
		req.Header.Set("Authorization", value)
		rec := serve(auth, ingest, req)

		require.Equal(t, http.StatusOK, rec.Code, value)
		require.Equal(t, int64(4), ingest.installation)
		require.Equal(t, forge.KindGiteaPush, ingest.env.Kind, "kind is guessed without an event header")
	}

	// A GitLab token does not open the GitVerse door.
	req := httptest.NewRequest(http.MethodPost, "/hook/gitverse", bytes.NewReader([]byte(gtPush)))
	req.Header.Set("Authorization", "good")
	rec := serve(fakeAuth{tokens: map[string]int64{"gitlab:good": 3}}, &fakeIngest{}, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
