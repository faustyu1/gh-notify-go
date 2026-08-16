// Package config loads bot configuration from a TOML file, applies
// environment overrides for secrets, and validates the result before the
// process is allowed to start.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Bot struct {
	Token    string `toml:"token"`
	Username string `toml:"username"`
	OwnerID  int64  `toml:"owner_id"`
}

type Database struct {
	URL string `toml:"url"`
}

type GitHub struct {
	AppID          int64  `toml:"app_id"`
	Slug           string `toml:"slug"`
	PrivateKeyPath string `toml:"private_key_path"`
	WebhookSecret  string `toml:"webhook_secret"`
}

type HTTP struct {
	Addr      string `toml:"addr"`
	PublicURL string `toml:"public_url"`
}

// Limits holds the tunables the spec calls out as estimates rather than
// constants, so an operator can adjust them without a rebuild.
type Limits struct {
	StarDebounce  time.Duration `toml:"star_debounce"`
	StarCooldown  time.Duration `toml:"star_cooldown"`
	ChatPerMinute int           `toml:"chat_per_minute"`
	Workers       int           `toml:"workers"`
}

type Config struct {
	Bot      Bot      `toml:"bot"`
	Database Database `toml:"database"`
	GitHub   GitHub   `toml:"github"`
	HTTP     HTTP     `toml:"http"`
	Limits   Limits   `toml:"limits"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}

	applyEnv(&cfg)
	applyDefaults(&cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("BOT_TOKEN"); v != "" {
		cfg.Bot.Token = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("GITHUB_WEBHOOK_SECRET"); v != "" {
		cfg.GitHub.WebhookSecret = v
	}
	if v := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); v != "" {
		cfg.GitHub.PrivateKeyPath = v
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Limits.StarDebounce == 0 {
		cfg.Limits.StarDebounce = 60 * time.Second
	}
	if cfg.Limits.StarCooldown == 0 {
		cfg.Limits.StarCooldown = 24 * time.Hour
	}
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
