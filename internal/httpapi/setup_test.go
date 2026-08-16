package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

type fakeClaimer struct {
	calls [][2]int64
	err   error
}

func (f *fakeClaimer) ClaimInstallation(_ context.Context, installationID, userID int64) error {
	f.calls = append(f.calls, [2]int64{installationID, userID})
	return f.err
}

// fakeStates spends tokens the way the database does: a token works once.
type fakeStates struct {
	tokens map[string]int64
	asked  []string
}

func newStates(token string, userID int64) *fakeStates {
	return &fakeStates{tokens: map[string]int64{token: userID}}
}

func (f *fakeStates) TakeInstallState(_ context.Context, token string) (int64, error) {
	f.asked = append(f.asked, token)
	userID, ok := f.tokens[token]
	if !ok {
		return 0, errors.New("install state is unknown or expired")
	}
	delete(f.tokens, token)
	return userID, nil
}

func TestSetupRedirectsBackToTelegram(t *testing.T) {
	handler := httpapi.NewSetupHandler(nil, nil, "gh_notify_bot")

	req := httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=tok&setup_action=install", nil)
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
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/setup?state=tok", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetupClaimsInstallationForTheTokenOwner(t *testing.T) {
	claimer := &fakeClaimer{}
	handler := httpapi.NewSetupHandler(claimer, newStates("tok", 42), "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=tok", nil))

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, [][2]int64{{7, 42}}, claimer.calls)
}

// The endpoint is unauthenticated, so a hand-written request must not be able
// to point an installation at a user of the attacker's choosing.
func TestSetupIgnoresAForgedState(t *testing.T) {
	claimer := &fakeClaimer{}
	states := newStates("tok", 42)
	handler := httpapi.NewSetupHandler(claimer, states, "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=1", nil))

	require.Equal(t, http.StatusFound, rec.Code, "the user still lands in the bot")
	require.Empty(t, claimer.calls, "nothing may be claimed on an unknown state")
}

func TestSetupTokenWorksOnlyOnce(t *testing.T) {
	claimer := &fakeClaimer{}
	handler := httpapi.NewSetupHandler(claimer, newStates("tok", 42), "gh_notify_bot")

	for range 2 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/github/setup?installation_id=7&state=tok", nil))
		require.Equal(t, http.StatusFound, rec.Code)
	}
	require.Len(t, claimer.calls, 1, "a replayed redirect must claim nothing")
}

func TestSetupWithoutStateClaimsNothing(t *testing.T) {
	claimer := &fakeClaimer{}
	states := newStates("tok", 42)
	handler := httpapi.NewSetupHandler(claimer, states, "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7", nil))

	require.Equal(t, http.StatusFound, rec.Code)
	require.Empty(t, claimer.calls)
	require.Empty(t, states.asked)
}

func TestSetupStillRedirectsWhenClaimFails(t *testing.T) {
	claimer := &fakeClaimer{err: context.DeadlineExceeded}
	handler := httpapi.NewSetupHandler(claimer, newStates("tok", 42), "gh_notify_bot")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=tok", nil))

	require.Equal(t, http.StatusFound, rec.Code)
}
