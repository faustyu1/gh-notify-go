package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	body := []byte(`{"zen":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/gh/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signBody("wrong", body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "d-1")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhookRejectsMissingHeaders(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/gh/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signBody("s3cret", body))
	// No X-GitHub-Event header.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookRejectsNonPost(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gh/webhook", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
