package ghapp_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignatureAcceptsValidHeader(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	require.NoError(t, ghapp.VerifySignature("s3cret", body, sign("s3cret", body)))
}

func TestVerifySignatureRejectsWrongSecret(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	err := ghapp.VerifySignature("s3cret", body, sign("other", body))
	require.ErrorIs(t, err, ghapp.ErrBadSignature)
}

func TestVerifySignatureRejectsMissingHeader(t *testing.T) {
	require.ErrorIs(t,
		ghapp.VerifySignature("s3cret", []byte(`{}`), ""),
		ghapp.ErrBadSignature)
}

func TestVerifySignatureRejectsWrongPrefix(t *testing.T) {
	body := []byte(`{}`)
	raw := sign("s3cret", body)
	require.ErrorIs(t,
		ghapp.VerifySignature("s3cret", body, "sha1="+raw[7:]),
		ghapp.ErrBadSignature)
}

func TestParseEnvelopeExtractsRoutingFields(t *testing.T) {
	body := []byte(`{
		"action": "opened",
		"repository": {"id": 42, "full_name": "acme/app"},
		"installation": {"id": 7}
	}`)

	env, err := ghapp.ParseEnvelope("pull_request", "d-1", body)
	require.NoError(t, err)
	require.Equal(t, "d-1", env.DeliveryID)
	require.Equal(t, "pull_request", env.Kind)
	require.Equal(t, "opened", env.Action)
	require.Equal(t, int64(42), env.RepoGitHubID)
	require.Equal(t, "acme/app", env.RepoFullName)
	require.Equal(t, int64(7), env.InstallationID)
}

func TestParseEnvelopeToleratesMissingOptionalFields(t *testing.T) {
	// A ping carries no action and no installation on some deliveries.
	env, err := ghapp.ParseEnvelope("ping", "d-2", []byte(`{"zen":"x"}`))
	require.NoError(t, err)
	require.Equal(t, "", env.Action)
	require.Equal(t, int64(0), env.InstallationID)
}

func TestParseEnvelopeRejectsInvalidJSON(t *testing.T) {
	_, err := ghapp.ParseEnvelope("push", "d-3", []byte(`{`))
	require.Error(t, err)
}
