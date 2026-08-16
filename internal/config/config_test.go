package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/config"
)

// setRequired fills every mandatory variable so a test can then override the
// one it cares about. t.Setenv restores the previous values afterwards.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BOT_TOKEN", "123:abc")
	t.Setenv("BOT_USERNAME", "gh_notify_bot")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("GITHUB_APP_ID", "777")
	t.Setenv("GITHUB_SLUG", "gh-notify")
	t.Setenv("GITHUB_PRIVATE_KEY_PATH", "secrets/app.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "s3cret")
	t.Setenv("PUBLIC_URL", "https://example.com")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "123:abc", cfg.Bot.Token)
	require.Equal(t, int64(777), cfg.GitHub.AppID)
	require.Equal(t, 20, cfg.Limits.ChatPerMinute)
	require.Equal(t, 4, cfg.Limits.Workers)
	require.Equal(t, ":8080", cfg.HTTP.Addr)
}

func TestLoadReadsEveryVariable(t *testing.T) {
	setRequired(t)
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("WORKERS", "8")
	t.Setenv("CHAT_PER_MINUTE", "30")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, ":9090", cfg.HTTP.Addr)
	require.Equal(t, 8, cfg.Limits.Workers)
	require.Equal(t, 30, cfg.Limits.ChatPerMinute)
}

func TestLoadReportsEveryMissingField(t *testing.T) {
	// No variable is set: every required field must be named at once.
	cfg, err := config.Load()
	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bot.token")
	require.Contains(t, err.Error(), "database.url")
	require.Contains(t, err.Error(), "github.app_id")
	require.Contains(t, err.Error(), "github.webhook_secret")
}

func TestLoadRejectsSQLiteURL(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "sqlite://local.db")

	_, err := config.Load()
	require.ErrorContains(t, err, "database.url must be a postgres:// URL")
}

func TestLoadNamesTheBadVariable(t *testing.T) {
	setRequired(t)
	t.Setenv("WORKERS", "many")

	_, err := config.Load()
	require.ErrorContains(t, err, "WORKERS")
}

func TestLoadIgnoresEmptyOptionals(t *testing.T) {
	setRequired(t)
	_, err := config.Load()
	require.NoError(t, err)
}
