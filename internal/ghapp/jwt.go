package ghapp

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInstallationUnavailable means the installation was suspended or removed
// on the GitHub side. Callers mark affected integrations broken rather than
// retrying forever.
var ErrInstallationUnavailable = errors.New("github installation unavailable")

// tokenSafetyMargin is how long before real expiry a cached token is treated
// as stale, so a token never expires mid-request.
const tokenSafetyMargin = 5 * time.Minute

// TokenCache persists minted installation tokens. The database implementation
// encrypts them; tests use an in-memory one.
type TokenCache interface {
	Get(ctx context.Context, installationID int64) (token string, expiresAt time.Time, err error)
	Put(ctx context.Context, installationID int64, token string, expiresAt time.Time) error
}

type TokenSource struct {
	// BaseURL is overridden in tests; production uses the GitHub API host.
	BaseURL string

	appID int64
	key   *rsa.PrivateKey
	http  *http.Client
	cache TokenCache
	now   func() time.Time
}

func NewTokenSource(
	appID int64, key *rsa.PrivateKey, httpClient *http.Client,
	cache TokenCache, now func() time.Time,
) *TokenSource {
	return &TokenSource{
		BaseURL: "https://api.github.com",
		appID:   appID,
		key:     key,
		http:    httpClient,
		cache:   cache,
		now:     now,
	}
}

// LoadPrivateKey reads a PEM private key and refuses one that other users on
// the host can read.
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat private key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf(
			"private key %s has permissions %#o, expected 0600", path, info.Mode().Perm())
	}

	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return key, nil
}

// AppJWT signs a short-lived App-level JWT. iat is backdated because GitHub
// rejects tokens whose iat is even slightly in the future relative to its own
// clock; exp stays under GitHub's 10 minute ceiling.
func (s *TokenSource) AppJWT() (string, error) {
	now := s.now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", s.appID),
	})
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signed, nil
}

func (s *TokenSource) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	cached, expiresAt, err := s.cache.Get(ctx, installationID)
	if err == nil && cached != "" && s.now().Add(tokenSafetyMargin).Before(expiresAt) {
		return cached, nil
	}

	token, newExpiry, err := s.mint(ctx, installationID)
	if err != nil {
		return "", err
	}
	if err := s.cache.Put(ctx, installationID, token, newExpiry); err != nil {
		return "", fmt.Errorf("cache installation token: %w", err)
	}
	return token, nil
}

func (s *TokenSource) mint(ctx context.Context, installationID int64) (string, time.Time, error) {
	appJWT, err := s.AppJWT()
	if err != nil {
		return "", time.Time{}, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", s.BaseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mint installation token: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return "", time.Time{}, ErrInstallationUnavailable
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", time.Time{}, fmt.Errorf(
			"mint installation token: status %d: %s", resp.StatusCode, body)
	}

	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("decode token response: %w", err)
	}
	return payload.Token, payload.ExpiresAt, nil
}
