package ghapp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// memCache is a TokenCache that records how often Put was called, so the
// tests can prove the cache is actually consulted.
type memCache struct {
	token   string
	expires time.Time
	puts    atomic.Int32
}

func (m *memCache) Get(context.Context, int64) (string, time.Time, error) {
	return m.token, m.expires, nil
}

func (m *memCache) Put(_ context.Context, _ int64, token string, exp time.Time) error {
	m.token, m.expires = token, exp
	m.puts.Add(1)
	return nil
}

func TestLoadPrivateKeyReadsPKCS1PEM(t *testing.T) {
	key := testKey(t)
	path := filepath.Join(t.TempDir(), "app.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	loaded, err := ghapp.LoadPrivateKey(path)
	require.NoError(t, err)
	require.Equal(t, key.N, loaded.N)
}

func TestLoadPrivateKeyRejectsWorldReadableFile(t *testing.T) {
	key := testKey(t)
	path := filepath.Join(t.TempDir(), "app.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o644))

	_, err := ghapp.LoadPrivateKey(path)
	require.ErrorContains(t, err, "permissions")
}

func TestAppJWTCarriesIssuerAndBackdatedIat(t *testing.T) {
	key := testKey(t)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	src := ghapp.NewTokenSource(777, key, http.DefaultClient, &memCache{},
		func() time.Time { return now })

	raw, err := src.AppJWT()
	require.NoError(t, err)

	// Claims are asserted explicitly below; library validation would compare
	// exp against the wall clock and reject the fixed test timestamp.
	parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithoutClaimsValidation())
	require.NoError(t, err)

	claims := parsed.Claims.(jwt.MapClaims)
	require.Equal(t, "777", claims["iss"])
	// GitHub rejects a JWT whose iat is in the future by even a second, so
	// it is deliberately backdated by 60s.
	require.EqualValues(t, now.Add(-60*time.Second).Unix(), int64(claims["iat"].(float64)))
	require.EqualValues(t, now.Add(9*time.Minute).Unix(), int64(claims["exp"].(float64)))
}

func TestInstallationTokenMintsAndCaches(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "/app/installations/7/access_tokens", r.URL.Path)
		require.Contains(t, r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_abc","expires_at":"2026-08-16T13:00:00Z"}`))
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	cache := &memCache{}
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(), cache,
		func() time.Time { return now })
	src.BaseURL = server.URL

	token, err := src.InstallationToken(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "ghs_abc", token)
	require.EqualValues(t, 1, cache.puts.Load())

	// Second call is served from cache, so the server is not hit again.
	token, err = src.InstallationToken(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "ghs_abc", token)
	require.EqualValues(t, 1, calls.Load())
}

func TestInstallationTokenRefreshesNearExpiry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_fresh","expires_at":"2026-08-16T14:00:00Z"}`))
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	// Cached token expires in 30s — inside the 5 minute safety margin.
	cache := &memCache{token: "ghs_stale", expires: now.Add(30 * time.Second)}
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(), cache,
		func() time.Time { return now })
	src.BaseURL = server.URL

	token, err := src.InstallationToken(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "ghs_fresh", token)
	require.EqualValues(t, 1, calls.Load())
}

func TestInstallationTokenSurfacesSuspendedInstallation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"This installation has been suspended"}`))
	}))
	t.Cleanup(server.Close)

	src := ghapp.NewTokenSource(777, testKey(t), server.Client(), &memCache{}, time.Now)
	src.BaseURL = server.URL

	_, err := src.InstallationToken(context.Background(), 7)
	require.ErrorIs(t, err, ghapp.ErrInstallationUnavailable)
}
