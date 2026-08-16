package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/config"
)

const validTOML = `
[bot]
token = "123:abc"
username = "gh_notify_bot"
owner_id = 42

[database]
url = "postgres://u:p@localhost:5432/db"

[github]
app_id = 777
slug = "gh-notify"
private_key_path = "secrets/app.pem"
webhook_secret = "s3cret"

[http]
addr = ":8080"
public_url = "https://example.com"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validTOML))
	require.NoError(t, err)

	require.Equal(t, "123:abc", cfg.Bot.Token)
	require.Equal(t, int64(777), cfg.GitHub.AppID)
	require.Equal(t, 60*time.Second, cfg.Limits.StarDebounce)
	require.Equal(t, 24*time.Hour, cfg.Limits.StarCooldown)
	require.Equal(t, 20, cfg.Limits.ChatPerMinute)
	require.Equal(t, 4, cfg.Limits.Workers)
}

func TestLoadEnvOverridesFile(t *testing.T) {
	t.Setenv("BOT_TOKEN", "999:zzz")
	t.Setenv("DATABASE_URL", "postgres://env/db")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "envsecret")

	cfg, err := config.Load(writeConfig(t, validTOML))
	require.NoError(t, err)

	require.Equal(t, "999:zzz", cfg.Bot.Token)
	require.Equal(t, "postgres://env/db", cfg.Database.URL)
	require.Equal(t, "envsecret", cfg.GitHub.WebhookSecret)
}

func TestLoadReportsEveryMissingField(t *testing.T) {
	_, err := config.Load(writeConfig(t, "[bot]\nusername = \"x\"\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "bot.token")
	require.Contains(t, err.Error(), "database.url")
	require.Contains(t, err.Error(), "github.app_id")
	require.Contains(t, err.Error(), "github.webhook_secret")
}

func TestLoadRejectsSQLiteURL(t *testing.T) {
	_, err := config.Load(writeConfig(t, validTOML+"\n"))
	require.NoError(t, err)

	t.Setenv("DATABASE_URL", "sqlite://local.db")
	_, err = config.Load(writeConfig(t, validTOML))
	require.ErrorContains(t, err, "database.url must be a postgres:// URL")
}
