package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

func TestSetupRedirectsBackToTelegram(t *testing.T) {
	handler := httpapi.NewSetupHandler(nil, nil, "gh_notify_bot")

	req := httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=42&setup_action=install", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t,
		"https://t.me/gh_notify_bot?start=installed_7",
		rec.Header().Get("Location"))
}

func TestSetupRejectsMissingInstallationID(t *testing.T) {
	handler := httpapi.NewSetupHandler(nil, nil, "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/setup?state=42", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
