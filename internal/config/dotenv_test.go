package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/config"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadDotEnvSetsMissingVariables(t *testing.T) {
	t.Setenv("ALREADY_SET", "from-env")

	path := writeEnv(t, `
# a comment
BOT_TOKEN=123:abc
QUOTED="postgres://u:p@h/db"
ALREADY_SET=from-file
`)
	require.NoError(t, config.LoadDotEnv(path))

	require.Equal(t, "123:abc", os.Getenv("BOT_TOKEN"))
	require.Equal(t, "postgres://u:p@h/db", os.Getenv("QUOTED"))
	require.Equal(t, "from-env", os.Getenv("ALREADY_SET"),
		"the real environment must win over the file")
}

func TestLoadDotEnvIgnoresMissingFile(t *testing.T) {
	require.NoError(t, config.LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")))
}
