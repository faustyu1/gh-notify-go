package secret_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/secret"
)

func newKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestRoundTrip(t *testing.T) {
	box, err := secret.NewBox(newKey(t))
	require.NoError(t, err)

	sealed, err := box.Seal([]byte("ghs_installationtoken"))
	require.NoError(t, err)
	require.NotContains(t, string(sealed), "ghs_")

	opened, err := box.Open(sealed)
	require.NoError(t, err)
	require.Equal(t, "ghs_installationtoken", string(opened))
}

func TestSealIsNotDeterministic(t *testing.T) {
	box, err := secret.NewBox(newKey(t))
	require.NoError(t, err)

	a, err := box.Seal([]byte("same"))
	require.NoError(t, err)
	b, err := box.Seal([]byte("same"))
	require.NoError(t, err)

	require.NotEqual(t, a, b, "a fresh nonce must be used per seal")
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, err := secret.NewBox(newKey(t))
	require.NoError(t, err)

	sealed, err := box.Seal([]byte("payload"))
	require.NoError(t, err)
	sealed[len(sealed)-1] ^= 0xFF

	_, err = box.Open(sealed)
	require.Error(t, err)
}

func TestNewBoxRejectsWrongKeySize(t *testing.T) {
	_, err := secret.NewBox(base64.StdEncoding.EncodeToString([]byte("short")))
	require.ErrorContains(t, err, "32 bytes")
}
