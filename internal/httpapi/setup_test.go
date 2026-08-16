package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

type fakeClaimer struct {
	calls [] [2]int64
	err   error
}

func (f *fakeClaimer) ClaimInstallation(_ context.Context, installationID, userID int64) error {
	f.calls = append(f.calls, [2]int64{installationID, userID})
	return f.err
}

func TestSetupRedirectsBackToTelegram(t *testing.T) {
	handler := httpapi.NewSetupHandler(nil, "gh_notify_bot")

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
	handler := httpapi.NewSetupHandler(nil, "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/setup?state=42", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetupClaimsInstallationFromState(t *testing.T) {
	claimer := &fakeClaimer{}
	handler := httpapi.NewSetupHandler(claimer, "gh_notify_bot")

	req := httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=42", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, [][2]int64{{7, 42}}, claimer.calls)
}

func TestSetupStillRedirectsWhenClaimFails(t *testing.T) {
	claimer := &fakeClaimer{err: context.DeadlineExceeded}
	handler := httpapi.NewSetupHandler(claimer, "gh_notify_bot")

	req := httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=42", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
}
