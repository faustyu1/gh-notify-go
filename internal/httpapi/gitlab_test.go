package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/gitlab"
	"github.com/faustyu/gh-notify-go/internal/httpapi"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

type fakeTokens map[string]int64

func (f fakeTokens) GitLabInstallationByToken(_ context.Context, token string) (int64, error) {
	if id, ok := f[token]; ok {
		return id, nil
	}
	return 0, storage.ErrUnknownWebhookToken
}

type fakeGitLabIngest struct {
	installation int64
	env          gitlab.Envelope
	calls        int
}

func (f *fakeGitLabIngest) HandleGitLab(
	_ context.Context, installationID int64, env gitlab.Envelope,
) (service.Result, error) {
	f.calls++
	f.installation, f.env = installationID, env
	return service.Result{}, nil
}

const glPush = `{"object_kind":"push","project_id":15,
	"project":{"id":15,"path_with_namespace":"mike/diaspora"}}`

func TestGitLabWebhookRejectsUnknownToken(t *testing.T) {
	ingest := &fakeGitLabIngest{}
	handler := httpapi.NewGitLabWebhookHandler(fakeTokens{"good": 3}, ingest)

	req := httptest.NewRequest(http.MethodPost, "/gl/webhook", bytes.NewReader([]byte(glPush)))
	req.Header.Set("X-Gitlab-Token", "bad")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Zero(t, ingest.calls)
}

func TestGitLabWebhookRoutesToConnection(t *testing.T) {
	ingest := &fakeGitLabIngest{}
	handler := httpapi.NewGitLabWebhookHandler(fakeTokens{"good": 3}, ingest)

	req := httptest.NewRequest(http.MethodPost, "/gl/webhook", bytes.NewReader([]byte(glPush)))
	req.Header.Set("X-Gitlab-Token", "good")
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(3), ingest.installation)
	require.Equal(t, gitlab.KindPush, ingest.env.Kind)
	require.Equal(t, int64(15), ingest.env.ProjectID)
}

func TestGitLabWebhookRejectsNonPost(t *testing.T) {
	handler := httpapi.NewGitLabWebhookHandler(fakeTokens{}, &fakeGitLabIngest{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gl/webhook", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
