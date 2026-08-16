// Package config assembles bot configuration from environment variables
// (usually loaded from a .env file) and validates it before the process is
// allowed to start.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Bot struct {
	Token    string
	Username string
	OwnerID  int64
}

type Database struct {
	URL string
}

type GitHub struct {
	AppID          int64
	Slug           string
	PrivateKeyPath string
	WebhookSecret  string
}

type HTTP struct {
	Addr      string
	PublicURL string
}

// Limits holds the tunables the spec calls out as estimates rather than
// constants, so an operator can adjust them without a rebuild.
type Limits struct {
	ChatPerMinute int
	Workers       int
}

type Config struct {
	Bot      Bot
	Database Database
	GitHub   GitHub
	HTTP     HTTP
	Limits   Limits
}

func Load() (*Config, error) {
	var cfg Config
	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnv(cfg *Config) error {
	cfg.Bot.Token = os.Getenv("BOT_TOKEN")
	cfg.Bot.Username = os.Getenv("BOT_USERNAME")
	cfg.Database.URL = os.Getenv("DATABASE_URL")
	cfg.GitHub.Slug = os.Getenv("GITHUB_SLUG")
	cfg.GitHub.PrivateKeyPath = os.Getenv("GITHUB_PRIVATE_KEY_PATH")
	cfg.GitHub.WebhookSecret = os.Getenv("GITHUB_WEBHOOK_SECRET")
	cfg.HTTP.Addr = os.Getenv("HTTP_ADDR")
	cfg.HTTP.PublicURL = os.Getenv("PUBLIC_URL")

	var err error
	if cfg.Bot.OwnerID, err = envInt64("BOT_OWNER_ID"); err != nil {
		return err
	}
	if cfg.GitHub.AppID, err = envInt64("GITHUB_APP_ID"); err != nil {
		return err
	}
	if cfg.Limits.ChatPerMinute, err = envInt("CHAT_PER_MINUTE"); err != nil {
		return err
	}
	if cfg.Limits.Workers, err = envInt("WORKERS"); err != nil {
		return err
	}
	return nil
}

func envInt64(key string) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func envInt(key string) (int, error) {
	v, err := envInt64(key)
	return int(v), err
}

func envDuration(key string) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Limits.ChatPerMinute == 0 {
		cfg.Limits.ChatPerMinute = 20
	}
	if cfg.Limits.Workers == 0 {
		cfg.Limits.Workers = 4
	}
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8080"
	}
}

// validate collects every problem instead of returning the first one, so a
// misconfigured deployment is fixed in one pass rather than one restart per
// missing field.
func (c *Config) validate() error {
	var problems []string

	require := func(ok bool, field string) {
		if !ok {
			problems = append(problems, field+" is required")
		}
	}

	require(c.Bot.Token != "", "bot.token")
	require(c.Bot.Username != "", "bot.username")
	require(c.Database.URL != "", "database.url")
	require(c.GitHub.AppID != 0, "github.app_id")
	require(c.GitHub.Slug != "", "github.slug")
	require(c.GitHub.PrivateKeyPath != "", "github.private_key_path")
	require(c.GitHub.WebhookSecret != "", "github.webhook_secret")
	require(c.HTTP.PublicURL != "", "http.public_url")

	if c.Database.URL != "" &&
		!strings.HasPrefix(c.Database.URL, "postgres://") &&
		!strings.HasPrefix(c.Database.URL, "postgresql://") {
		problems = append(problems, "database.url must be a postgres:// URL")
	}

	if len(problems) > 0 {
		return errors.New("invalid config: " + strings.Join(problems, "; "))
	}
	return nil
}
