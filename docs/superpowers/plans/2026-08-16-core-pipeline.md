# Core Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a running Telegram bot that a user installs the GitHub App into, connects a repository to a group chat through inline-keyboard screens, and then receives `push`, `pull_request`, and `issues` notifications in that chat.

**Architecture:** One Go binary. A GitHub webhook handler verifies HMAC, deduplicates by delivery ID, resolves matching integrations, and inserts rows into a Postgres `outbox` table. A worker pool drains the outbox with `SELECT … FOR UPDATE SKIP LOCKED`, renders each event to Telegram HTML, and sends it with retry and backoff. Telegram updates arrive by long polling and drive a screen engine that edits a single anchor message in the user's DM.

**Tech Stack:** Go 1.26, `github.com/mymmrac/telego` v1.11.1, `github.com/jackc/pgx/v5` v5.10.0, `sqlc` (pgx/v5 driver), `github.com/pressly/goose/v3` v3.27.3, `github.com/golang-jwt/jwt/v5`, `github.com/BurntSushi/toml`, `github.com/testcontainers/testcontainers-go`, `github.com/stretchr/testify/require`.

**Spec:** `docs/superpowers/specs/2026-08-16-gh-notify-go-design.md`

## Global Constraints

- Go 1.26. Module path `github.com/faustyu/gh-notify-go`.
- PostgreSQL only. No SQLite fallback anywhere.
- GitHub App authentication only. No personal access tokens, no OAuth. There must be no `token` column on `users` and no per-repository webhook creation code.
- The bot exposes exactly one Telegram command, `/start`. Every other interaction is an inline keyboard button or a `ForceReply` text input.
- Premium emoji appear only in message text as `<tg-emoji emoji-id="…">FALLBACK</tg-emoji>` under `ParseMode: "HTML"`. Button labels are plain Unicode — Telegram does not support entities in button text.
- Emoji IDs are referenced only through named constants in `internal/events/render/emoji.go`. A raw emoji ID literal anywhere else is a defect.
- Every screen except `home` renders a `◁ Назад` button.
- `event_settings` and `filters` are keyed by `integration_id`, never by `chat_id`.
- `star.deleted` is never rendered into a message.
- The GitHub webhook handler must not call the Telegram API. It only writes to the database.
- Secrets in the database are encrypted with AES-GCM using the key from the `SECRET_KEY` environment variable (32 bytes, base64-encoded).
- Tests are written before implementation. Every task ends with a commit.

---

## File Structure

| File | Responsibility |
|---|---|
| `cmd/bot/main.go` | Wire dependencies, start poller + HTTP server + workers, graceful shutdown |
| `internal/config/config.go` | Load and validate TOML + environment overrides |
| `internal/domain/types.go` | Pure types: `EventKind`, `Integration`, `Chat`, `OutboxRow` and friends |
| `internal/secret/aesgcm.go` | Encrypt/decrypt values stored in the database |
| `internal/storage/migrations/*.sql` | goose schema migrations |
| `internal/storage/queries/*.sql` | sqlc query definitions |
| `internal/storage/gen/*` | sqlc-generated code (never hand-edited) |
| `internal/storage/store.go` | `Store` wrapper over the generated queries + pool lifecycle |
| `internal/storage/testhelper/pg.go` | testcontainers Postgres harness shared by all storage tests |
| `internal/ghapp/jwt.go` | App JWT signing, installation token minting, cached lookup |
| `internal/ghapp/client.go` | GitHub REST calls: installations, repositories, commit compare |
| `internal/ghapp/webhook.go` | HMAC verification and webhook envelope parsing |
| `internal/events/registry.go` | Event kind registry, parse + render dispatch |
| `internal/events/render/html.go` | Escaping, links, truncation, `tg-emoji` wrapping |
| `internal/events/render/emoji.go` | Named premium emoji ID constants |
| `internal/events/push.go`, `pull_request.go`, `issues.go` | One file per event type |
| `internal/outbox/enqueue.go` | Insert rows, apply dedup and grouping |
| `internal/outbox/worker.go` | Claim, render, send, retry, backoff, terminal failure |
| `internal/tg/sender.go` | Telegram send with splitting, topic fallback, error classification |
| `internal/tg/ui/engine.go` | Screen registry, navigation stack, callback indirection |
| `internal/tg/ui/screens/*.go` | One file per screen |
| `internal/service/ingest.go` | Webhook payload → matching integrations → outbox rows |
| `internal/service/integrate.go` | Connect a repository to a chat, admin re-check |
| `deploy/` | Dockerfile, docker-compose.yml, Caddyfile.example |

---

## Task 1: Project skeleton and configuration

**Files:**
- Create: `go.mod`, `Makefile`, `config.example.toml`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Load(path string) (*config.Config, error)`; struct `config.Config` with fields `Bot config.Bot`, `Database config.Database`, `GitHub config.GitHub`, `HTTP config.HTTP`, `Limits config.Limits`. `Bot` has `Token string`, `Username string`, `OwnerID int64`. `Database` has `URL string`. `GitHub` has `AppID int64`, `Slug string`, `PrivateKeyPath string`, `WebhookSecret string`. `HTTP` has `Addr string`, `PublicURL string`. `Limits` has `StarDebounce time.Duration`, `StarCooldown time.Duration`, `ChatPerMinute int`, `Workers int`.

- [ ] **Step 1: Initialise the module**

```bash
cd /Users/faustyu/dev/gh-notify-go
go mod init github.com/faustyu/gh-notify-go
go get github.com/BurntSushi/toml@latest
go get github.com/stretchr/testify@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 3: Run the test and confirm it fails**

Run: `go test ./internal/config/...`
Expected: FAIL — the `config` package does not exist.

- [ ] **Step 4: Implement the config package**

Create `internal/config/config.go`:

```go
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
```

- [ ] **Step 5: Run the test and confirm it passes**

Run: `go test ./internal/config/...`
Expected: PASS, four tests.

- [ ] **Step 6: Add the example config and Makefile**

Create `config.example.toml`:

```toml
[bot]
token = ""          # or BOT_TOKEN
username = ""       # bot @username without @
owner_id = 0        # Telegram id of the operator

[database]
url = "postgres://gh:gh@localhost:5432/ghnotify?sslmode=disable"

[github]
app_id = 0
slug = ""
private_key_path = "secrets/app.pem"
webhook_secret = ""  # or GITHUB_WEBHOOK_SECRET

[http]
addr = ":8080"
public_url = "https://example.com"

[limits]
star_debounce = "60s"
star_cooldown = "24h"
chat_per_minute = 20
workers = 4
```

Create `Makefile`:

```make
.PHONY: test lint build sqlc migrate-new

test:
	go test ./...

build:
	go build -o bin/bot ./cmd/bot

sqlc:
	sqlc generate

migrate-new:
	goose -dir internal/storage/migrations create $(NAME) sql
```

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum Makefile config.example.toml internal/config
git commit -m "feat: config loading with env overrides and startup validation"
```

---

## Task 2: Database schema and test harness

**Files:**
- Create: `internal/storage/migrations/00001_init.sql`
- Create: `internal/storage/migrations/embed.go`
- Create: `internal/storage/testhelper/pg.go`
- Test: `internal/storage/migrations_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `migrations.FS embed.FS`; `migrations.Up(ctx context.Context, dbURL string) error`; `testhelper.StartPostgres(t *testing.T) string` returning a migrated database URL.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/pressly/goose/v3@v3.27.3
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/storage/migrations_test.go`:

```go
package storage_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func TestMigrationsCreateExpectedTables(t *testing.T) {
	ctx := context.Background()
	url := testhelper.StartPostgres(t)

	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	want := []string{
		"users", "installations", "chats", "chat_managers", "integrations",
		"event_settings", "filters", "outbox", "star_actors",
		"gh_deliveries", "ui_actions", "ui_nav", "audit_log",
	}
	for _, table := range want {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = $1)`, table,
		).Scan(&exists)
		require.NoError(t, err)
		require.Truef(t, exists, "table %q should exist", table)
	}
}

func TestIntegrationsRejectDuplicateRepoInChat(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID, chatID, installID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (1) RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO chats (telegram_chat_id, title, kind) VALUES (-100, 't', 'supergroup')
		 RETURNING id`).Scan(&chatID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO installations (github_installation_id, account_login, account_type, user_id)
		 VALUES (5, 'acme', 'Organization', $1) RETURNING id`, userID).Scan(&installID))

	insert := `INSERT INTO integrations
		(chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		VALUES ($1, $2, 99, 'acme/app', $3)`
	_, err = pool.Exec(ctx, insert, chatID, installID, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, insert, chatID, installID, userID)
	require.ErrorContains(t, err, "duplicate key")
}
```

- [ ] **Step 3: Run the test and confirm it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `internal/storage/testhelper` does not exist.

- [ ] **Step 4: Write the migration**

Create `internal/storage/migrations/00001_init.sql`:

```sql
-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    telegram_id   BIGINT      NOT NULL UNIQUE,
    github_login  TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE installations (
    id                     BIGSERIAL PRIMARY KEY,
    github_installation_id BIGINT      NOT NULL UNIQUE,
    account_login          TEXT        NOT NULL,
    account_type           TEXT        NOT NULL,
    user_id                BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_ciphertext       BYTEA,
    token_expires_at       TIMESTAMPTZ,
    suspended_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX installations_user_idx ON installations (user_id);

CREATE TABLE chats (
    id               BIGSERIAL PRIMARY KEY,
    telegram_chat_id BIGINT      NOT NULL UNIQUE,
    title            TEXT        NOT NULL DEFAULT '',
    kind             TEXT        NOT NULL,
    topic_id         BIGINT,
    muted_until      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Who brought the bot into a chat. Without this the chat picker has nothing
-- to offer before the first integration exists, and listing every known chat
-- would leak other people's groups. Admin rights are still re-checked against
-- Telegram at connect time; this table only bounds the candidate list.
CREATE TABLE chat_managers (
    chat_id    BIGINT      NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE integrations (
    id                 BIGSERIAL PRIMARY KEY,
    chat_id            BIGINT      NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    installation_id    BIGINT      NOT NULL REFERENCES installations (id) ON DELETE CASCADE,
    repo_github_id     BIGINT      NOT NULL,
    repo_full_name     TEXT        NOT NULL,
    created_by_user_id BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    broken_reason      TEXT,
    last_event_at      TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, repo_github_id)
);
-- The hot lookup on every incoming webhook.
CREATE INDEX integrations_repo_install_idx
    ON integrations (repo_github_id, installation_id);

CREATE TABLE event_settings (
    integration_id BIGINT  NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    event_kind     TEXT    NOT NULL,
    enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (integration_id, event_kind)
);

CREATE TABLE filters (
    id             BIGSERIAL PRIMARY KEY,
    integration_id BIGINT      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    kind           TEXT        NOT NULL,
    pattern        TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX filters_integration_idx ON filters (integration_id);

CREATE TABLE outbox (
    id              BIGSERIAL PRIMARY KEY,
    chat_id         BIGINT      NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    integration_id  BIGINT      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    event_kind      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    group_key       TEXT,
    status          TEXT        NOT NULL DEFAULT 'pending',
    scheduled_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);
-- Drives the worker claim query.
CREATE INDEX outbox_claim_idx ON outbox (status, scheduled_at)
    WHERE status = 'pending';
-- Lets the star debounce find a pending row for one actor cheaply.
CREATE INDEX outbox_group_idx ON outbox (group_key)
    WHERE status = 'pending' AND group_key IS NOT NULL;

CREATE TABLE star_actors (
    integration_id   BIGINT      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    actor_login      TEXT        NOT NULL,
    last_notified_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (integration_id, actor_login)
);

CREATE TABLE gh_deliveries (
    delivery_id TEXT        PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX gh_deliveries_received_idx ON gh_deliveries (received_at);

CREATE TABLE ui_actions (
    key        TEXT        PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    screen     TEXT        NOT NULL,
    params     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ui_actions_user_idx ON ui_actions (user_id, created_at);

CREATE TABLE ui_nav (
    user_id    BIGINT      PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    message_id BIGINT,
    stack      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id            BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    chat_id       BIGINT      REFERENCES chats (id) ON DELETE SET NULL,
    action        TEXT        NOT NULL,
    meta          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_chat_idx ON audit_log (chat_id, created_at DESC);

-- +goose Down
DROP TABLE audit_log, ui_nav, ui_actions, gh_deliveries, star_actors,
           outbox, filters, event_settings, integrations, chat_managers,
           chats, installations, users;
```

- [ ] **Step 5: Write the migration runner**

Create `internal/storage/migrations/embed.go`:

```go
// Package migrations embeds the goose schema migrations and applies them.
package migrations

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// Up applies every pending migration. It opens its own database/sql handle
// because goose needs one; the application itself uses pgxpool.
func Up(ctx context.Context, dbURL string) error {
	db, err := stdlib.OpenDB(*mustParse(dbURL))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
```

Add the helper in the same file:

```go
func mustParse(dbURL string) *pgx.ConnConfig {
	cfg, err := pgx.ParseConfig(dbURL)
	if err != nil {
		panic(fmt.Sprintf("invalid database url: %v", err))
	}
	return cfg
}
```

Add `"github.com/jackc/pgx/v5"` to the import block.

- [ ] **Step 6: Write the test harness**

Create `internal/storage/testhelper/pg.go`:

```go
// Package testhelper starts a throwaway Postgres for storage tests and
// applies the real migrations to it, so tests exercise the actual schema
// rather than a hand-maintained copy.
package testhelper

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/faustyu/gh-notify-go/internal/storage/migrations"
)

// StartPostgres boots a container, migrates it, and returns its URL. The
// container is torn down when the test finishes.
func StartPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("ghnotify"),
		tcpostgres.WithUsername("gh"),
		tcpostgres.WithPassword("gh"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = testcontainers.TerminateContainer(container)
	})

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	require.NoError(t, migrations.Up(ctx, url))
	return url
}
```

- [ ] **Step 7: Run the tests and confirm they pass**

Run: `go test ./internal/storage/...`
Expected: PASS, two tests. First run pulls the `postgres:17-alpine` image, so allow a minute.

- [ ] **Step 8: Commit**

```bash
git add internal/storage go.mod go.sum
git commit -m "feat: postgres schema, goose runner, testcontainers harness"
```

---

## Task 3: Secret encryption

**Files:**
- Create: `internal/secret/aesgcm.go`
- Test: `internal/secret/aesgcm_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `secret.NewBox(base64Key string) (*secret.Box, error)`; `(*secret.Box).Seal(plaintext []byte) ([]byte, error)`; `(*secret.Box).Open(ciphertext []byte) ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/secret/aesgcm_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/secret/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the package**

Create `internal/secret/aesgcm.go`:

```go
// Package secret encrypts values that are stored in the database. Only the
// installation-token cache uses it today; the GitHub private key stays on
// disk and the webhook secret stays in the config file.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

type Box struct {
	aead cipher.AEAD
}

// NewBox builds an AES-256-GCM box from a base64-encoded 32-byte key,
// normally supplied through the SECRET_KEY environment variable.
func NewBox(base64Key string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce||ciphertext so the nonce travels with the value and no
// separate column is needed.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	size := b.aead.NonceSize()
	if len(ciphertext) < size {
		return nil, errors.New("ciphertext shorter than nonce")
	}
	plaintext, err := b.aead.Open(nil, ciphertext[:size], ciphertext[size:], nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return plaintext, nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/secret/...`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/secret
git commit -m "feat: AES-GCM box for secrets stored in the database"
```

---

## Task 4: Webhook signature verification and envelope parsing

**Files:**
- Create: `internal/ghapp/webhook.go`
- Test: `internal/ghapp/webhook_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ghapp.VerifySignature(secret string, body []byte, header string) error`; `ghapp.ErrBadSignature`; type `ghapp.Envelope` with fields `DeliveryID string`, `Kind string`, `Action string`, `RepoGitHubID int64`, `RepoFullName string`, `InstallationID int64`, `Raw json.RawMessage`; `ghapp.ParseEnvelope(kind, deliveryID string, body []byte) (ghapp.Envelope, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/ghapp/webhook_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/ghapp/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement webhook verification and parsing**

Create `internal/ghapp/webhook.go`:

```go
// Package ghapp holds everything specific to GitHub App authentication and
// the App-level webhook.
package ghapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrBadSignature = errors.New("invalid webhook signature")

// VerifySignature checks the X-Hub-Signature-256 header. The comparison is
// constant time so a forged signature cannot be discovered byte by byte.
func VerifySignature(secret string, body []byte, header string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrBadSignature
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return ErrBadSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrBadSignature
	}
	return nil
}

// Envelope is the subset of every webhook payload the router needs. The full
// payload stays in Raw and is decoded later by the per-event parser.
type Envelope struct {
	DeliveryID     string
	Kind           string
	Action         string
	RepoGitHubID   int64
	RepoFullName   string
	InstallationID int64
	Raw            json.RawMessage
}

type envelopeShape struct {
	Action     string `json:"action"`
	Repository *struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func ParseEnvelope(kind, deliveryID string, body []byte) (Envelope, error) {
	var shape envelopeShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return Envelope{}, fmt.Errorf("parse envelope: %w", err)
	}

	env := Envelope{
		DeliveryID: deliveryID,
		Kind:       kind,
		Action:     shape.Action,
		Raw:        json.RawMessage(body),
	}
	if shape.Repository != nil {
		env.RepoGitHubID = shape.Repository.ID
		env.RepoFullName = shape.Repository.FullName
	}
	if shape.Installation != nil {
		env.InstallationID = shape.Installation.ID
	}
	return env, nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/ghapp/...`
Expected: PASS, seven tests.

- [ ] **Step 5: Commit**

```bash
git add internal/ghapp
git commit -m "feat: webhook HMAC verification and envelope parsing"
```

---

## Task 5: App JWT and installation tokens

**Files:**
- Create: `internal/ghapp/jwt.go`
- Test: `internal/ghapp/jwt_test.go`

**Interfaces:**
- Consumes: `secret.Box` from Task 3.
- Produces: `ghapp.LoadPrivateKey(path string) (*rsa.PrivateKey, error)`; `ghapp.NewTokenSource(appID int64, key *rsa.PrivateKey, http *http.Client, cache ghapp.TokenCache, now func() time.Time) *ghapp.TokenSource`; `(*TokenSource).AppJWT() (string, error)`; `(*TokenSource).InstallationToken(ctx context.Context, installationID int64) (string, error)`. Interface `ghapp.TokenCache` with `Get(ctx context.Context, installationID int64) (token string, expiresAt time.Time, err error)` and `Put(ctx context.Context, installationID int64, token string, expiresAt time.Time) error`.

- [ ] **Step 1: Add the JWT dependency**

```bash
go get github.com/golang-jwt/jwt/v5@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/ghapp/jwt_test.go`:

```go
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

	parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return &key.PublicKey, nil })
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
```

- [ ] **Step 3: Run the test and confirm it fails**

Run: `go test ./internal/ghapp/ -run 'TestLoadPrivateKey|TestAppJWT|TestInstallationToken'`
Expected: FAIL — `LoadPrivateKey` and `NewTokenSource` are undefined.

- [ ] **Step 4: Implement the token source**

Create `internal/ghapp/jwt.go`:

```go
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
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/ghapp/...`
Expected: PASS, thirteen tests.

- [ ] **Step 6: Commit**

```bash
git add internal/ghapp go.mod go.sum
git commit -m "feat: github app jwt and cached installation tokens"
```

---

## Task 6: GitHub REST client

**Files:**
- Create: `internal/ghapp/client.go`
- Test: `internal/ghapp/client_test.go`

**Interfaces:**
- Consumes: `ghapp.TokenSource` from Task 5.
- Produces: `ghapp.NewClient(src *ghapp.TokenSource, httpClient *http.Client) *ghapp.Client`; `(*Client).ListInstallations(ctx context.Context, userAccessToken string) ([]ghapp.Account, error)` is **not** part of this task; instead `(*Client).ListRepositories(ctx context.Context, installationID int64) ([]ghapp.Repository, error)` and `(*Client).CompareStats(ctx context.Context, installationID int64, repoFullName, base, head string) (ghapp.DiffStat, error)`. Types: `ghapp.Repository{GitHubID int64, FullName string, Private bool, Description string}`, `ghapp.DiffStat{Additions, Deletions, ChangedFiles int}`.

- [ ] **Step 1: Write the failing test**

Create `internal/ghapp/client_test.go`:

```go
package ghapp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

// newClient wires a TokenSource whose cache already holds a valid token, so
// these tests exercise the REST calls rather than the minting path.
func newClient(t *testing.T, handler http.Handler) *ghapp.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	cache := &memCache{token: "ghs_valid", expires: now.Add(time.Hour)}
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(), cache,
		func() time.Time { return now })
	src.BaseURL = server.URL

	client := ghapp.NewClient(src, server.Client())
	client.BaseURL = server.URL
	return client
}

func TestListRepositoriesFollowsPagination(t *testing.T) {
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token ghs_valid", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
				{"id":3,"full_name":"acme/c","private":false,"description":""}]}`))
			return
		}
		w.Header().Set("Link",
			fmt.Sprintf(`<%s/installation/repositories?page=2>; rel="next"`, serverURL))
		_, _ = w.Write([]byte(`{"total_count":3,"repositories":[
			{"id":1,"full_name":"acme/a","private":true,"description":"first"},
			{"id":2,"full_name":"acme/b","private":false,"description":""}]}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	serverURL = server.URL

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	src := ghapp.NewTokenSource(777, testKey(t), server.Client(),
		&memCache{token: "ghs_valid", expires: now.Add(time.Hour)},
		func() time.Time { return now })
	src.BaseURL = server.URL
	client := ghapp.NewClient(src, server.Client())
	client.BaseURL = server.URL

	repos, err := client.ListRepositories(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, repos, 3)
	require.Equal(t, "acme/a", repos[0].FullName)
	require.True(t, repos[0].Private)
	require.Equal(t, "acme/c", repos[2].FullName)
}

func TestCompareStatsReturnsTotals(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/acme/app/compare/aaa...bbb", r.URL.Path)
		_, _ = w.Write([]byte(`{"files":[
			{"additions":10,"deletions":2},
			{"additions":1,"deletions":0}]}`))
	}))

	stat, err := client.CompareStats(context.Background(), 7, "acme/app", "aaa", "bbb")
	require.NoError(t, err)
	require.Equal(t, 11, stat.Additions)
	require.Equal(t, 2, stat.Deletions)
	require.Equal(t, 2, stat.ChangedFiles)
}

func TestCompareStatsTreatsMissingCompareAsEmpty(t *testing.T) {
	// A force-push can leave a base commit GitHub no longer knows about.
	// That must degrade to "no stats", not fail the whole notification.
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	stat, err := client.CompareStats(context.Background(), 7, "acme/app", "aaa", "bbb")
	require.NoError(t, err)
	require.Equal(t, ghapp.DiffStat{}, stat)
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/ghapp/ -run 'TestListRepositories|TestCompareStats'`
Expected: FAIL — `ghapp.NewClient` is undefined.

- [ ] **Step 3: Implement the client**

Create `internal/ghapp/client.go`:

```go
package ghapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

type Repository struct {
	GitHubID    int64
	FullName    string
	Private     bool
	Description string
}

type DiffStat struct {
	Additions    int
	Deletions    int
	ChangedFiles int
}

type Client struct {
	BaseURL string

	tokens *TokenSource
	http   *http.Client
}

func NewClient(tokens *TokenSource, httpClient *http.Client) *Client {
	return &Client{BaseURL: "https://api.github.com", tokens: tokens, http: httpClient}
}

var nextLinkRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// ListRepositories returns every repository the installation can see,
// following Link-header pagination.
func (c *Client) ListRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	url := c.BaseURL + "/installation/repositories?per_page=100"
	var out []Repository

	for url != "" {
		var page struct {
			Repositories []struct {
				ID          int64  `json:"id"`
				FullName    string `json:"full_name"`
				Private     bool   `json:"private"`
				Description string `json:"description"`
			} `json:"repositories"`
		}

		next, err := c.get(ctx, installationID, url, &page)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Repositories {
			out = append(out, Repository{
				GitHubID:    r.ID,
				FullName:    r.FullName,
				Private:     r.Private,
				Description: r.Description,
			})
		}
		url = next
	}
	return out, nil
}

// CompareStats sums the per-file counts of a commit range. A missing range
// (force push, deleted commit) yields a zero DiffStat rather than an error,
// because the notification is still worth sending without the numbers.
func (c *Client) CompareStats(
	ctx context.Context, installationID int64, repoFullName, base, head string,
) (DiffStat, error) {
	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", c.BaseURL, repoFullName, base, head)

	var payload struct {
		Files []struct {
			Additions int `json:"additions"`
			Deletions int `json:"deletions"`
		} `json:"files"`
	}
	if _, err := c.get(ctx, installationID, url, &payload); err != nil {
		if errorsIsNotFound(err) {
			return DiffStat{}, nil
		}
		return DiffStat{}, err
	}

	stat := DiffStat{ChangedFiles: len(payload.Files)}
	for _, f := range payload.Files {
		stat.Additions += f.Additions
		stat.Deletions += f.Deletions
	}
	return stat, nil
}

type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("github status %d", e.code) }

func errorsIsNotFound(err error) bool {
	var se statusError
	return asStatusError(err, &se) && se.code == http.StatusNotFound
}

func asStatusError(err error, target *statusError) bool {
	se, ok := err.(statusError)
	if ok {
		*target = se
	}
	return ok
}

// get performs one authenticated request and returns the "next" page URL if
// the response carries one.
func (c *Client) get(ctx context.Context, installationID int64, url string, out any) (string, error) {
	token, err := c.tokens.InstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return "", statusError{code: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", fmt.Errorf("decode github response: %w", err)
	}

	if m := nextLinkRe.FindStringSubmatch(resp.Header.Get("Link")); len(m) == 2 {
		return m[1], nil
	}
	return "", nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/ghapp/...`
Expected: PASS, sixteen tests.

- [ ] **Step 5: Commit**

```bash
git add internal/ghapp
git commit -m "feat: github rest client for repositories and commit diffs"
```

---

## Task 7: HTML rendering helpers and the emoji registry

**Files:**
- Create: `internal/events/render/html.go`
- Create: `internal/events/render/emoji.go`
- Test: `internal/events/render/html_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `render.Escape(s string) string`; `render.Link(url, text string) string`; `render.Truncate(s string, limit int) string`; `render.Emoji(id, fallback string) string`; `render.Strip(html string) string`; constants `render.EmojiSettings`, `render.EmojiProfile`, `render.EmojiPeople`, `render.EmojiFile`, `render.EmojiChart`, `render.EmojiStats`, `render.EmojiHouse`, `render.EmojiLockClosed`, `render.EmojiLockOpen`, `render.EmojiMegaphone`, `render.EmojiCheck`, `render.EmojiCross`, `render.EmojiPencil`, `render.EmojiTrash`, `render.EmojiLink`, `render.EmojiInfo`, `render.EmojiBot`, `render.EmojiEye`, `render.EmojiBell`, `render.EmojiClock`, `render.EmojiParty`, `render.EmojiWrite`, `render.EmojiMedia`, `render.EmojiCalendar`, `render.EmojiTag`, `render.EmojiCode`, `render.EmojiLoading`.

- [ ] **Step 1: Write the failing test**

Create `internal/events/render/html_test.go`:

```go
package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

func TestEscapeCoversTelegramSpecialCharacters(t *testing.T) {
	require.Equal(t, "&lt;b&gt;x&lt;/b&gt; &amp; y", render.Escape("<b>x</b> & y"))
}

func TestLinkEscapesBothPartsSeparately(t *testing.T) {
	got := render.Link("https://x.dev/a?b=1&c=2", "pull <request>")
	require.Equal(t,
		`<a href="https://x.dev/a?b=1&amp;c=2">pull &lt;request&gt;</a>`, got)
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	require.Equal(t, "short", render.Truncate("short", 10))
}

func TestTruncateCutsOnRuneBoundary(t *testing.T) {
	// Cyrillic is two bytes per rune; a byte-based cut would corrupt it.
	got := render.Truncate("привет мир", 6)
	require.Equal(t, "привет…", got)
	require.True(t, strings.HasSuffix(got, "…"))
}

func TestEmojiWrapsIDWithUnicodeFallback(t *testing.T) {
	require.Equal(t,
		`<tg-emoji emoji-id="5870982283724328568">⚙</tg-emoji>`,
		render.Emoji(render.EmojiSettings, "⚙"))
}

func TestStripRemovesOnlyEmojiTags(t *testing.T) {
	in := `<b>hi</b> <tg-emoji emoji-id="123">⚙</tg-emoji> <a href="u">l</a>`
	require.Equal(t, `<b>hi</b> ⚙ <a href="u">l</a>`, render.Strip(in))
}

func TestEmojiConstantsAreDistinct(t *testing.T) {
	ids := []string{
		render.EmojiSettings, render.EmojiProfile, render.EmojiPeople,
		render.EmojiFile, render.EmojiChart, render.EmojiStats,
		render.EmojiHouse, render.EmojiLockClosed, render.EmojiLockOpen,
		render.EmojiMegaphone, render.EmojiCheck, render.EmojiCross,
		render.EmojiPencil, render.EmojiTrash, render.EmojiLink,
		render.EmojiInfo, render.EmojiBot, render.EmojiEye,
		render.EmojiBell, render.EmojiClock, render.EmojiParty,
		render.EmojiWrite, render.EmojiMedia, render.EmojiCalendar,
		render.EmojiTag, render.EmojiCode, render.EmojiLoading,
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		require.NotEmpty(t, id)
		require.Falsef(t, seen[id], "emoji id %s used twice", id)
		seen[id] = true
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/events/render/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the emoji registry**

Create `internal/events/render/emoji.go`:

```go
package render

// Premium custom emoji ids supplied by the operator. Referencing them by
// name keeps the raw ids in one place; a literal id anywhere else in the
// codebase is a defect.
const (
	EmojiSettings   = "5870982283724328568"
	EmojiProfile    = "5870994129244131212"
	EmojiPeople     = "5870772616305839506"
	EmojiPersonYes  = "5891207662678317861"
	EmojiPersonNo   = "5893192487324880883"
	EmojiFile       = "5870528606328852614"
	EmojiSmile      = "5870764288364252592"
	EmojiChart      = "5870930636742595124"
	EmojiStats      = "5870921681735781843"
	EmojiHouse      = "5873147866364514353"
	EmojiLockClosed = "6037249452824072506"
	EmojiLockOpen   = "6037496202990194718"
	EmojiMegaphone  = "6039422865189638057"
	EmojiCheck      = "5870633910337015697"
	EmojiCross      = "5870657884844462243"
	EmojiPencil     = "5870676941614354370"
	EmojiTrash      = "5870875489362513438"
	EmojiDown       = "5893057118545646106"
	EmojiClip       = "6039451237743595514"
	EmojiLink       = "5769289093221454192"
	EmojiInfo       = "6028435952299413210"
	EmojiBot        = "6030400221232501136"
	EmojiEye        = "6037397706505195857"
	EmojiHidden     = "6037243349675544634"
	EmojiUpload     = "5963103826075456248"
	EmojiDownload   = "6039802767931871481"
	EmojiBell       = "6039486778597970865"
	EmojiGift       = "6032644646587338669"
	EmojiClock      = "5983150113483134607"
	EmojiParty      = "6041731551845159060"
	EmojiFont       = "5870801517140775623"
	EmojiWrite      = "5870753782874246579"
	EmojiMedia      = "6035128606563241721"
	EmojiPin        = "6042011682497106307"
	EmojiWallet     = "5769126056262898415"
	EmojiBox        = "5884479287171485878"
	EmojiCalendar   = "5890937706803894250"
	EmojiTag        = "5886285355279193209"
	EmojiElapsed    = "5775896410780079073"
	EmojiApps       = "5778672437122045013"
	EmojiBrush      = "6050679691004612757"
	EmojiAddText    = "5771851822897566479"
	EmojiFormat     = "5778479949572738874"
	EmojiCode       = "5940433880585605708"
	EmojiLoading    = "5345906554510012647"
)
```

- [ ] **Step 4: Implement the HTML helpers**

Create `internal/events/render/html.go`:

```go
// Package render builds Telegram-HTML fragments. Telegram accepts a small
// tag subset, so everything here emits only b, i, code, pre, a and tg-emoji.
package render

import (
	"fmt"
	"regexp"
	"strings"
)

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Escape makes arbitrary text safe to place inside Telegram HTML.
func Escape(s string) string { return escaper.Replace(s) }

// Link builds an anchor, escaping the URL and the label independently.
func Link(url, text string) string {
	return fmt.Sprintf(`<a href="%s">%s</a>`, Escape(url), Escape(text))
}

// Truncate cuts on a rune boundary and appends an ellipsis. Cutting by byte
// would split multi-byte characters and produce invalid UTF-8 in the message.
func Truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}

// Emoji wraps a premium emoji id together with the Unicode character clients
// without custom-emoji support will render instead.
func Emoji(id, fallback string) string {
	return fmt.Sprintf(`<tg-emoji emoji-id="%s">%s</tg-emoji>`, id, fallback)
}

var emojiTagRe = regexp.MustCompile(`<tg-emoji emoji-id="[^"]*">(.*?)</tg-emoji>`)

// Strip replaces every tg-emoji tag with its fallback text. The sender calls
// this once when Telegram rejects custom emoji entities, so the message still
// goes out looking plain instead of not going out at all.
func Strip(html string) string {
	return emojiTagRe.ReplaceAllString(html, "$1")
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/events/render/...`
Expected: PASS, seven tests.

- [ ] **Step 6: Commit**

```bash
git add internal/events/render
git commit -m "feat: telegram html helpers and premium emoji registry"
```

---

## Task 8: Event registry and the push event

**Files:**
- Create: `internal/events/registry.go`
- Create: `internal/events/push.go`
- Create: `internal/events/testdata/push.json`
- Create: `internal/events/testdata/push.golden.html`
- Test: `internal/events/registry_test.go`
- Test: `internal/events/golden_test.go`

**Interfaces:**
- Consumes: `render` helpers from Task 7.
- Produces: type `events.Kind string`; type `events.Renderer func(raw json.RawMessage) (string, error)`; `events.Register(kind events.Kind, action events.ActionFilter, r events.Renderer)`; `events.Render(kind events.Kind, raw json.RawMessage) (string, error)`; `events.Wanted(kind events.Kind, action string) bool`; `events.Kinds() []events.Kind`; `events.ErrUnknownKind`. Type `events.ActionFilter []string` where an empty slice means every action is wanted.

- [ ] **Step 1: Write the failing registry test**

Create `internal/events/registry_test.go`:

```go
package events_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
)

func TestRenderUnknownKindIsAnError(t *testing.T) {
	_, err := events.Render("no_such_event", json.RawMessage(`{}`))
	require.ErrorIs(t, err, events.ErrUnknownKind)
}

func TestWantedHonoursActionFilter(t *testing.T) {
	// push registers with an empty filter: every delivery is wanted.
	require.True(t, events.Wanted("push", ""))
	require.True(t, events.Wanted("push", "anything"))
}

func TestWantedRejectsUnknownKind(t *testing.T) {
	require.False(t, events.Wanted("no_such_event", "opened"))
}

func TestKindsIncludesRegisteredEvents(t *testing.T) {
	require.Contains(t, events.Kinds(), events.Kind("push"))
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/events/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the registry**

Create `internal/events/registry.go`:

```go
// Package events turns GitHub webhook payloads into Telegram HTML. Each
// event type lives in its own file and registers itself in init(), so adding
// a type is one new file and no edits elsewhere.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

type Kind string

// Renderer turns a raw payload into a ready Telegram HTML message.
type Renderer func(raw json.RawMessage) (string, error)

// ActionFilter lists the payload actions worth sending. An empty filter
// means the event has no action field, or every action is worth sending.
type ActionFilter []string

var ErrUnknownKind = errors.New("unknown event kind")

type registration struct {
	filter ActionFilter
	render Renderer
}

var (
	mu       sync.RWMutex
	registry = map[Kind]registration{}
)

// Register wires one event type. Registering the same kind twice panics,
// because it means two files disagree about who owns the type.
func Register(kind Kind, filter ActionFilter, r Renderer) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("events: kind %q registered twice", kind))
	}
	registry[kind] = registration{filter: filter, render: r}
}

// Wanted reports whether this kind+action should produce a message at all.
// The ingest path calls it before touching the database.
func Wanted(kind Kind, action string) bool {
	mu.RLock()
	defer mu.RUnlock()

	reg, ok := registry[kind]
	if !ok {
		return false
	}
	if len(reg.filter) == 0 {
		return true
	}
	return slices.Contains(reg.filter, action)
}

func Render(kind Kind, raw json.RawMessage) (string, error) {
	mu.RLock()
	reg, ok := registry[kind]
	mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	return reg.render(raw)
}

// Kinds returns every registered kind in a stable order, used to build the
// event-toggle screen.
func Kinds() []Kind {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

- [ ] **Step 4: Write the golden-test harness and the push fixture**

Create `internal/events/golden_test.go`:

```go
package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
)

// -update rewrites the golden files. Review the diff before committing:
// a golden test only protects you if the expected output is read by a human
// at least once.
var update = flag.Bool("update", false, "rewrite golden files")

// assertGolden renders testdata/<name>.json and compares it to
// testdata/<name>.golden.html.
func assertGolden(t *testing.T, kind events.Kind, name string) {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	got, err := events.Render(kind, json.RawMessage(payload))
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", name+".golden.html")
	if *update {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

func TestPushGolden(t *testing.T) {
	assertGolden(t, "push", "push")
}
```

Create `internal/events/testdata/push.json`:

```json
{
  "ref": "refs/heads/main",
  "before": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "after": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "created": false,
  "deleted": false,
  "forced": false,
  "compare": "https://github.com/acme/app/compare/aaaaaaa...bbbbbbb",
  "repository": {
    "id": 42,
    "full_name": "acme/app",
    "html_url": "https://github.com/acme/app"
  },
  "pusher": { "name": "octocat" },
  "sender": { "login": "octocat", "html_url": "https://github.com/octocat" },
  "commits": [
    {
      "id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "message": "fix: handle <nil> config\n\nlonger body ignored",
      "url": "https://github.com/acme/app/commit/bbbbbbb",
      "author": { "name": "Octo Cat", "username": "octocat" }
    },
    {
      "id": "cccccccccccccccccccccccccccccccccccccccc",
      "message": "chore: bump deps",
      "url": "https://github.com/acme/app/commit/ccccccc",
      "author": { "name": "Octo Cat", "username": "octocat" }
    }
  ]
}
```

- [ ] **Step 5: Run the golden test and confirm it fails**

Run: `go test ./internal/events/ -run TestPushGolden`
Expected: FAIL — `unknown event kind: push`.

- [ ] **Step 6: Implement the push event**

Create `internal/events/push.go`:

```go
package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

// maxCommitsListed caps how many commit lines a single push renders. Beyond
// this the message says how many were omitted rather than growing unbounded.
const maxCommitsListed = 10

type pushPayload struct {
	Ref     string `json:"ref"`
	Forced  bool   `json:"forced"`
	Compare string `json:"compare"`
	Repo    struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
	} `json:"commits"`
}

func init() {
	// push has no action field, so every delivery is wanted.
	Register("push", nil, renderPush)
}

func renderPush(raw json.RawMessage) (string, error) {
	var p pushPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse push: %w", err)
	}

	branch := strings.TrimPrefix(p.Ref, "refs/heads/")

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiUpload, "⬆"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")

	verb := "запушил"
	if p.Forced {
		verb = "форс-запушил"
	}
	b.WriteString(fmt.Sprintf("%s %s в %s — %s\n\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		verb,
		"<code>"+render.Escape(branch)+"</code>",
		pluralCommits(len(p.Commits)),
	))

	shown := p.Commits
	if len(shown) > maxCommitsListed {
		shown = shown[:maxCommitsListed]
	}
	for _, c := range shown {
		title, _, _ := strings.Cut(c.Message, "\n")
		b.WriteString(fmt.Sprintf("• %s %s\n",
			render.Link(c.URL, shortSHA(c.ID)),
			render.Escape(render.Truncate(title, 72)),
		))
	}
	if omitted := len(p.Commits) - len(shown); omitted > 0 {
		b.WriteString(fmt.Sprintf("…и ещё %d\n", omitted))
	}

	if p.Compare != "" {
		b.WriteString("\n" + render.Link(p.Compare, "Посмотреть изменения"))
	}
	return b.String(), nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// pluralCommits produces the correct Russian plural form, which depends on
// the last digit and the teens exception.
func pluralCommits(n int) string {
	word := "коммитов"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "коммит"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		word = "коммита"
	}
	return fmt.Sprintf("%d %s", n, word)
}
```

- [ ] **Step 7: Generate and review the golden file**

```bash
go test ./internal/events/ -run TestPushGolden -update
cat internal/events/testdata/push.golden.html
```

Read the output. It must contain `<tg-emoji emoji-id="5963103826075456248">⬆</tg-emoji>`, the branch as `<code>main</code>`, `2 коммита`, and both commit links. If it does not, fix `renderPush` and regenerate — do not edit the golden file by hand.

- [ ] **Step 8: Run the whole package without -update**

Run: `go test ./internal/events/`
Expected: PASS, five tests.

- [ ] **Step 9: Commit**

```bash
git add internal/events
git commit -m "feat: event registry and push renderer with golden test"
```

---

## Task 9: pull_request and issues events

**Files:**
- Create: `internal/events/pull_request.go`
- Create: `internal/events/issues.go`
- Create: `internal/events/testdata/pull_request.json`, `pull_request_merged.json`, `issues.json`
- Modify: `internal/events/golden_test.go` (add three test functions)

**Interfaces:**
- Consumes: `Register`, `render` helpers.
- Produces: registrations for kinds `pull_request` (filter `opened`, `closed`, `reopened`, `ready_for_review`) and `issues` (filter `opened`, `closed`, `reopened`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/events/golden_test.go`:

```go
func TestPullRequestOpenedGolden(t *testing.T) {
	assertGolden(t, "pull_request", "pull_request")
}

func TestPullRequestMergedGolden(t *testing.T) {
	assertGolden(t, "pull_request", "pull_request_merged")
}

func TestIssuesGolden(t *testing.T) {
	assertGolden(t, "issues", "issues")
}

func TestPullRequestActionFilter(t *testing.T) {
	require.True(t, events.Wanted("pull_request", "opened"))
	require.True(t, events.Wanted("pull_request", "ready_for_review"))
	// Label churn is the single noisiest PR action; it must not be sent.
	require.False(t, events.Wanted("pull_request", "labeled"))
	require.False(t, events.Wanted("pull_request", "synchronize"))
}

func TestIssuesActionFilter(t *testing.T) {
	require.True(t, events.Wanted("issues", "opened"))
	require.False(t, events.Wanted("issues", "labeled"))
}
```

Create `internal/events/testdata/pull_request.json`:

```json
{
  "action": "opened",
  "number": 128,
  "pull_request": {
    "html_url": "https://github.com/acme/app/pull/128",
    "title": "Add <retry> to the sender",
    "body": "Fixes flaky delivery.",
    "merged": false,
    "draft": false,
    "additions": 42,
    "deletions": 7,
    "changed_files": 3,
    "base": { "ref": "main" },
    "head": { "ref": "feat/retry" },
    "user": { "login": "octocat", "html_url": "https://github.com/octocat" }
  },
  "repository": { "id": 42, "full_name": "acme/app" },
  "sender": { "login": "octocat", "html_url": "https://github.com/octocat" }
}
```

Create `internal/events/testdata/pull_request_merged.json`: same as above with
`"action": "closed"`, `"merged": true`, and `"title": "Add retry to the sender"`.

Create `internal/events/testdata/issues.json`:

```json
{
  "action": "opened",
  "issue": {
    "number": 9,
    "html_url": "https://github.com/acme/app/issues/9",
    "title": "Crash on empty config",
    "body": "Steps to reproduce…",
    "user": { "login": "octocat", "html_url": "https://github.com/octocat" }
  },
  "repository": { "id": 42, "full_name": "acme/app" },
  "sender": { "login": "octocat", "html_url": "https://github.com/octocat" }
}
```

- [ ] **Step 2: Run and confirm they fail**

Run: `go test ./internal/events/ -run 'PullRequest|Issues'`
Expected: FAIL — `unknown event kind: pull_request`.

- [ ] **Step 3: Implement pull_request**

Create `internal/events/pull_request.go`:

```go
package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type pullRequestPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		HTMLURL      string `json:"html_url"`
		Title        string `json:"title"`
		Merged       bool   `json:"merged"`
		Draft        bool   `json:"draft"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		Base         struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login   string `json:"login"`
			HTMLURL string `json:"html_url"`
		} `json:"user"`
	} `json:"pull_request"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func init() {
	Register("pull_request",
		ActionFilter{"opened", "closed", "reopened", "ready_for_review"},
		renderPullRequest)
}

func renderPullRequest(raw json.RawMessage) (string, error) {
	var p pullRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse pull_request: %w", err)
	}

	emoji, verb := pullRequestHeadline(p.Action, p.PullRequest.Merged, p.PullRequest.Draft)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s %s пул-реквест %s\n\n",
		render.Link(p.PullRequest.User.HTMLURL, p.PullRequest.User.Login),
		verb,
		render.Link(p.PullRequest.HTMLURL, fmt.Sprintf("#%d", p.Number)),
	))
	b.WriteString("<b>" + render.Escape(render.Truncate(p.PullRequest.Title, 120)) + "</b>\n")
	b.WriteString(fmt.Sprintf("<code>%s</code> → <code>%s</code>\n",
		render.Escape(p.PullRequest.Head.Ref),
		render.Escape(p.PullRequest.Base.Ref),
	))

	if p.PullRequest.ChangedFiles > 0 {
		b.WriteString(fmt.Sprintf("%s +%d −%d в %d файлах\n",
			render.Emoji(render.EmojiFile, "📁"),
			p.PullRequest.Additions,
			p.PullRequest.Deletions,
			p.PullRequest.ChangedFiles,
		))
	}
	return b.String(), nil
}

// pullRequestHeadline picks the icon and verb. "closed" splits into merged
// and rejected, which are very different outcomes and must not look alike.
func pullRequestHeadline(action string, merged, draft bool) (string, string) {
	switch {
	case action == "closed" && merged:
		return render.Emoji(render.EmojiCheck, "✅"), "вмержил"
	case action == "closed":
		return render.Emoji(render.EmojiCross, "❌"), "закрыл"
	case action == "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "переоткрыл"
	case action == "ready_for_review":
		return render.Emoji(render.EmojiEye, "👁"), "снял черновик с"
	case draft:
		return render.Emoji(render.EmojiPencil, "🖋"), "открыл черновик"
	default:
		return render.Emoji(render.EmojiWrite, "✍"), "открыл"
	}
}
```

- [ ] **Step 4: Implement issues**

Create `internal/events/issues.go`:

```go
package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type issuesPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		User    struct {
			Login   string `json:"login"`
			HTMLURL string `json:"html_url"`
		} `json:"user"`
	} `json:"issue"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("issues", ActionFilter{"opened", "closed", "reopened"}, renderIssues)
}

func renderIssues(raw json.RawMessage) (string, error) {
	var p issuesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse issues: %w", err)
	}

	emoji, verb := issuesHeadline(p.Action)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s %s issue %s\n\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		verb,
		render.Link(p.Issue.HTMLURL, fmt.Sprintf("#%d", p.Issue.Number)),
	))
	b.WriteString("<b>" + render.Escape(render.Truncate(p.Issue.Title, 120)) + "</b>")
	return b.String(), nil
}

func issuesHeadline(action string) (string, string) {
	switch action {
	case "closed":
		return render.Emoji(render.EmojiCheck, "✅"), "закрыл"
	case "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "переоткрыл"
	default:
		return render.Emoji(render.EmojiMegaphone, "📣"), "открыл"
	}
}
```

- [ ] **Step 5: Generate and review the golden files**

```bash
go test ./internal/events/ -run 'PullRequest|Issues' -update
cat internal/events/testdata/pull_request.golden.html
cat internal/events/testdata/pull_request_merged.golden.html
cat internal/events/testdata/issues.golden.html
```

Check by eye: the opened PR uses the ✍ emoji, the merged one uses ✅ and the
word `вмержил`, and the `<retry>` in the title appears escaped as
`&lt;retry&gt;`. Regenerate rather than hand-editing if anything is wrong.

- [ ] **Step 6: Run the full package**

Run: `go test ./internal/events/...`
Expected: PASS, ten tests.

- [ ] **Step 7: Commit**

```bash
git add internal/events
git commit -m "feat: pull_request and issues renderers with golden tests"
```

---

## Deviation from the spec: hand-written pgx instead of sqlc

The spec names `sqlc` for database access. This plan uses hand-written `pgx`
queries behind explicit Go methods instead, for one reason: every task after
this one references storage method names and parameter types, and generated
names would only be known after running a code generator that is not yet
installed. Hand-written methods let each task state its exact signature up
front, which is what makes the tasks independently implementable.

The trade is real — hand-written SQL is not compile-time checked against the
schema. It is covered by the storage tests running against a real migrated
Postgres, which catches the same class of error one layer later. If you prefer
`sqlc`, swap it in after Task 10; nothing above the `Store` interface changes.

---

## Task 10: Storage layer

**Files:**
- Create: `internal/domain/types.go`
- Create: `internal/storage/store.go`
- Create: `internal/storage/users.go`
- Create: `internal/storage/integrations.go`
- Create: `internal/storage/tokencache.go`
- Test: `internal/storage/store_test.go`

**Interfaces:**
- Consumes: `secret.Box` (Task 3), `testhelper.StartPostgres` (Task 2).
- Produces:
  - `domain.Chat{ID int64, TelegramChatID int64, Title string, Kind string, TopicID *int64, MutedUntil *time.Time}`
  - `domain.Integration{ID int64, ChatID int64, TelegramChatID int64, TopicID *int64, InstallationID int64, GitHubInstallationID int64, RepoGitHubID int64, RepoFullName string, CreatedByUserID int64, OwnerTelegramID int64}`
  - `storage.New(ctx context.Context, dbURL string, box *secret.Box) (*storage.Store, error)`; `(*Store).Close()`; `(*Store).Pool() *pgxpool.Pool`
  - `(*Store).UpsertUser(ctx context.Context, telegramID int64) (int64, error)`
  - `(*Store).UpsertChat(ctx context.Context, telegramChatID int64, title, kind string) (int64, error)`
  - `(*Store).UpsertInstallation(ctx context.Context, githubInstallationID int64, login, accountType string, userID int64) (int64, error)`
  - `(*Store).CreateIntegration(ctx context.Context, chatID, installationID, repoGitHubID int64, repoFullName string, createdByUserID int64) (int64, error)`
  - `(*Store).IntegrationsForRepo(ctx context.Context, repoGitHubID, githubInstallationID int64) ([]domain.Integration, error)`
  - `(*Store).EventEnabled(ctx context.Context, integrationID int64, kind string) (bool, error)`
  - `(*Store).TokenCache() ghapp.TokenCache`

- [ ] **Step 1: Write the failing test**

Create `internal/storage/store_test.go`:

```go
package storage_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func newStore(t *testing.T) *storage.Store {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	box, err := secret.NewBox(base64.StdEncoding.EncodeToString(key))
	require.NoError(t, err)

	store, err := storage.New(context.Background(), testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

func TestUpsertUserIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	first, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	second, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestUpsertChatRefreshesTitle(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	id, err := store.UpsertChat(ctx, -100, "Old name", "supergroup")
	require.NoError(t, err)
	again, err := store.UpsertChat(ctx, -100, "New name", "supergroup")
	require.NoError(t, err)
	require.Equal(t, id, again)

	var title string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT title FROM chats WHERE id = $1`, id).Scan(&title))
	require.Equal(t, "New name", title)
}

func TestIntegrationsForRepoJoinsChatAndOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)
	_, err = store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, int64(-100), found[0].TelegramChatID)
	require.Equal(t, "acme/app", found[0].RepoFullName)
	require.Equal(t, int64(555), found[0].OwnerTelegramID)
	require.Nil(t, found[0].TopicID)
}

func TestIntegrationsForRepoSkipsMutedChats(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	_, err = store.Pool().Exec(ctx,
		`UPDATE chats SET muted_until = now() + interval '1 hour' WHERE id = $1`, chatID)
	require.NoError(t, err)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestEventEnabledDefaultsToTrueWithNoRow(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	// No event_settings row exists yet: a new integration is fully on.
	enabled, err := store.EventEnabled(ctx, integrationID, "push")
	require.NoError(t, err)
	require.True(t, enabled)

	_, err = store.Pool().Exec(ctx,
		`INSERT INTO event_settings (integration_id, event_kind, enabled)
		 VALUES ($1, 'push', false)`, integrationID)
	require.NoError(t, err)

	enabled, err = store.EventEnabled(ctx, integrationID, "push")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestTokenCacheRoundTripsEncrypted(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	_, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	cache := store.TokenCache()
	require.NoError(t, cache.Put(ctx, 7, "ghs_secret_value", expires))

	// The plaintext must not be readable straight out of the column.
	var stored []byte
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT token_ciphertext FROM installations
		 WHERE github_installation_id = 7`).Scan(&stored))
	require.NotContains(t, string(stored), "ghs_secret_value")

	token, gotExpires, err := cache.Get(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "ghs_secret_value", token)
	require.Equal(t, expires, gotExpires.UTC().Truncate(time.Second))
}

func TestTokenCacheGetReturnsEmptyWhenAbsent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	token, _, err := store.TokenCache().Get(ctx, 999)
	require.NoError(t, err)
	require.Empty(t, token)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/storage/ -run 'TestUpsert|TestIntegrations|TestEventEnabled|TestTokenCache'`
Expected: FAIL — `storage.New` is undefined.

- [ ] **Step 3: Define the domain types**

Create `internal/domain/types.go`:

```go
// Package domain holds the types shared across services. It imports no
// infrastructure, so services built on it are testable without a database.
package domain

import "time"

type Chat struct {
	ID             int64
	TelegramChatID int64
	Title          string
	Kind           string
	TopicID        *int64
	MutedUntil     *time.Time
}

// Integration is the denormalized view the delivery path needs: enough to
// address a Telegram message and to notify the owner when delivery fails,
// without a second query per event.
type Integration struct {
	ID                   int64
	ChatID               int64
	TelegramChatID       int64
	TopicID              *int64
	InstallationID       int64
	GitHubInstallationID int64
	RepoGitHubID         int64
	RepoFullName         string
	CreatedByUserID      int64
	OwnerTelegramID      int64
}
```

- [ ] **Step 4: Implement the store**

Create `internal/storage/store.go`:

```go
// Package storage owns every SQL statement in the application. Nothing above
// this package writes SQL.
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faustyu/gh-notify-go/internal/secret"
)

type Store struct {
	pool *pgxpool.Pool
	box  *secret.Box
}

func New(ctx context.Context, dbURL string, box *secret.Box) (*Store, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool, box: box}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool is exposed for tests and for the outbox worker, which needs explicit
// transaction control that the typed methods do not provide.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
```

Create `internal/storage/users.go`:

```go
package storage

import (
	"context"
	"fmt"
)

func (s *Store) UpsertUser(ctx context.Context, telegramID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (telegram_id) VALUES ($1)
		ON CONFLICT (telegram_id) DO UPDATE SET telegram_id = EXCLUDED.telegram_id
		RETURNING id`, telegramID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert user: %w", err)
	}
	return id, nil
}

// UpsertChat refreshes the cached title on every call, because a group can be
// renamed and a stale title in the chat picker is confusing.
func (s *Store) UpsertChat(ctx context.Context, telegramChatID int64, title, kind string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chats (telegram_chat_id, title, kind) VALUES ($1, $2, $3)
		ON CONFLICT (telegram_chat_id)
		DO UPDATE SET title = EXCLUDED.title, kind = EXCLUDED.kind
		RETURNING id`, telegramChatID, title, kind).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert chat: %w", err)
	}
	return id, nil
}

func (s *Store) UpsertInstallation(
	ctx context.Context, githubInstallationID int64, login, accountType string, userID int64,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO installations
			(github_installation_id, account_login, account_type, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (github_installation_id) DO UPDATE
		SET account_login = EXCLUDED.account_login,
		    account_type  = EXCLUDED.account_type,
		    user_id       = EXCLUDED.user_id,
		    suspended_at  = NULL
		RETURNING id`, githubInstallationID, login, accountType, userID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert installation: %w", err)
	}
	return id, nil
}
```

Create `internal/storage/integrations.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/domain"
)

func (s *Store) CreateIntegration(
	ctx context.Context, chatID, installationID, repoGitHubID int64,
	repoFullName string, createdByUserID int64,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO integrations
			(chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		chatID, installationID, repoGitHubID, repoFullName, createdByUserID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create integration: %w", err)
	}
	return id, nil
}

// IntegrationsForRepo returns every live integration a webhook should fan out
// to. Muted and broken ones are excluded here so the ingest path stays a
// single query.
func (s *Store) IntegrationsForRepo(
	ctx context.Context, repoGitHubID, githubInstallationID int64,
) ([]domain.Integration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.chat_id, c.telegram_chat_id, c.topic_id,
		       i.installation_id, ins.github_installation_id,
		       i.repo_github_id, i.repo_full_name,
		       i.created_by_user_id, u.telegram_id
		FROM integrations i
		JOIN chats c          ON c.id  = i.chat_id
		JOIN installations ins ON ins.id = i.installation_id
		JOIN users u          ON u.id  = i.created_by_user_id
		WHERE i.repo_github_id = $1
		  AND ins.github_installation_id = $2
		  AND i.broken_reason IS NULL
		  AND (c.muted_until IS NULL OR c.muted_until <= now())`,
		repoGitHubID, githubInstallationID)
	if err != nil {
		return nil, fmt.Errorf("query integrations: %w", err)
	}
	defer rows.Close()

	var out []domain.Integration
	for rows.Next() {
		var it domain.Integration
		if err := rows.Scan(
			&it.ID, &it.ChatID, &it.TelegramChatID, &it.TopicID,
			&it.InstallationID, &it.GitHubInstallationID,
			&it.RepoGitHubID, &it.RepoFullName,
			&it.CreatedByUserID, &it.OwnerTelegramID,
		); err != nil {
			return nil, fmt.Errorf("scan integration: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// EventEnabled treats a missing row as enabled, so a fresh integration
// delivers everything and the settings table only stores deviations.
func (s *Store) EventEnabled(ctx context.Context, integrationID int64, kind string) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT enabled FROM event_settings
		WHERE integration_id = $1 AND event_kind = $2`, integrationID, kind).Scan(&enabled)

	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read event setting: %w", err)
	}
	return enabled, nil
}
```

Create `internal/storage/tokencache.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

type tokenCache struct{ store *Store }

// TokenCache returns the encrypted installation-token cache backed by the
// installations table.
func (s *Store) TokenCache() ghapp.TokenCache { return tokenCache{store: s} }

func (c tokenCache) Get(ctx context.Context, installationID int64) (string, time.Time, error) {
	var (
		ciphertext []byte
		expiresAt  *time.Time
	)
	err := c.store.pool.QueryRow(ctx, `
		SELECT token_ciphertext, token_expires_at FROM installations
		WHERE github_installation_id = $1`, installationID).Scan(&ciphertext, &expiresAt)

	if errors.Is(err, pgx.ErrNoRows) || len(ciphertext) == 0 || expiresAt == nil {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token cache: %w", err)
	}

	plaintext, err := c.store.box.Open(ciphertext)
	if err != nil {
		// A key rotation invalidates old ciphertext. Treat it as a cache
		// miss rather than an outage: the token is re-minted.
		return "", time.Time{}, nil
	}
	return string(plaintext), *expiresAt, nil
}

func (c tokenCache) Put(
	ctx context.Context, installationID int64, token string, expiresAt time.Time,
) error {
	ciphertext, err := c.store.box.Seal([]byte(token))
	if err != nil {
		return fmt.Errorf("seal token: %w", err)
	}
	_, err = c.store.pool.Exec(ctx, `
		UPDATE installations
		SET token_ciphertext = $2, token_expires_at = $3
		WHERE github_installation_id = $1`, installationID, ciphertext, expiresAt)
	if err != nil {
		return fmt.Errorf("write token cache: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/storage/...`
Expected: PASS, nine tests.

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/storage
git commit -m "feat: storage layer for users, chats, integrations and token cache"
```

---

## Task 11: Outbox enqueue and delivery deduplication

**Files:**
- Create: `internal/outbox/enqueue.go`
- Test: `internal/outbox/enqueue_test.go`

**Interfaces:**
- Consumes: `storage.Store` (Task 10).
- Produces: `outbox.NewQueue(pool *pgxpool.Pool, now func() time.Time) *outbox.Queue`; `(*Queue).MarkDelivered(ctx context.Context, deliveryID string) (fresh bool, err error)`; `(*Queue).Enqueue(ctx context.Context, row outbox.Row) (int64, error)`; type `outbox.Row{ChatID, IntegrationID int64, Kind string, Payload json.RawMessage, GroupKey string, Delay time.Duration}`; `(*Queue).PruneDeliveries(ctx context.Context, olderThan time.Duration) (int64, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/outbox/enqueue_test.go`:

```go
package outbox_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// fixture creates one user, chat, installation and integration and returns
// the pool plus the integration and chat ids every outbox row needs.
func fixture(t *testing.T) (*pgxpool.Pool, int64, int64) {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID, chatID, installID, integrationID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (555) RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO chats (telegram_chat_id, title, kind)
		 VALUES (-100, 'Team', 'supergroup') RETURNING id`).Scan(&chatID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO installations (github_installation_id, account_login, account_type, user_id)
		 VALUES (7, 'acme', 'Organization', $1) RETURNING id`, userID).Scan(&installID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO integrations
		   (chat_id, installation_id, repo_github_id, repo_full_name, created_by_user_id)
		 VALUES ($1, $2, 42, 'acme/app', $3) RETURNING id`,
		chatID, installID, userID).Scan(&integrationID))

	return pool, chatID, integrationID
}

func TestMarkDeliveredIsFreshOnlyOnce(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	fresh, err := queue.MarkDelivered(ctx, "delivery-1")
	require.NoError(t, err)
	require.True(t, fresh)

	// GitHub retries the same delivery: the second call must report it as
	// already seen so the handler returns 200 without fanning out again.
	fresh, err = queue.MarkDelivered(ctx, "delivery-1")
	require.NoError(t, err)
	require.False(t, fresh)
}

func TestEnqueueStoresPayloadAndSchedule(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID:        chatID,
		IntegrationID: integrationID,
		Kind:          "push",
		Payload:       json.RawMessage(`{"ref":"refs/heads/main"}`),
		Delay:         90 * time.Second,
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	var (
		status      string
		scheduledAt time.Time
		payload     []byte
		groupKey    *string
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, scheduled_at, payload, group_key FROM outbox WHERE id = $1`, id).
		Scan(&status, &scheduledAt, &payload, &groupKey))

	require.Equal(t, "pending", status)
	require.Equal(t, now.Add(90*time.Second), scheduledAt.UTC())
	require.JSONEq(t, `{"ref":"refs/heads/main"}`, string(payload))
	require.Nil(t, groupKey, "an empty GroupKey must be stored as NULL")
}

func TestEnqueueWithoutDelayIsImmediate(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "issues", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	var scheduledAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT scheduled_at FROM outbox WHERE id = $1`, id).Scan(&scheduledAt))
	require.Equal(t, now, scheduledAt.UTC())
}

func TestEnqueueStoresGroupKey(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "star", Payload: json.RawMessage(`{}`),
		GroupKey: "star:1:octocat",
	})
	require.NoError(t, err)

	var groupKey string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT group_key FROM outbox WHERE id = $1`, id).Scan(&groupKey))
	require.Equal(t, "star:1:octocat", groupKey)
}

func TestPruneDeliveriesRemovesOldRowsOnly(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := fixture(t)
	queue := outbox.NewQueue(pool, time.Now)

	_, err := pool.Exec(ctx,
		`INSERT INTO gh_deliveries (delivery_id, received_at)
		 VALUES ('old', now() - interval '10 days'), ('new', now())`)
	require.NoError(t, err)

	removed, err := queue.PruneDeliveries(ctx, 7*24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)

	var remaining string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT delivery_id FROM gh_deliveries`).Scan(&remaining))
	require.Equal(t, "new", remaining)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/outbox/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the queue**

Create `internal/outbox/enqueue.go`:

```go
// Package outbox is the durable delivery queue. The webhook handler writes to
// it and answers GitHub; workers drain it and talk to Telegram. Nothing else
// bridges those two sides.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Row struct {
	ChatID        int64
	IntegrationID int64
	Kind          string
	Payload       json.RawMessage

	// GroupKey ties rows that may cancel or merge with each other, such as
	// every pending star from one actor on one integration. Empty means the
	// row stands alone.
	GroupKey string

	// Delay holds the row back, which is what makes the star debounce
	// possible: the row exists but is not yet eligible for delivery.
	Delay time.Duration
}

type Queue struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewQueue(pool *pgxpool.Pool, now func() time.Time) *Queue {
	return &Queue{pool: pool, now: now}
}

// MarkDelivered records a GitHub delivery id and reports whether it is new.
// GitHub retries deliveries, so this is what keeps one push from producing
// two messages.
func (q *Queue) MarkDelivered(ctx context.Context, deliveryID string) (bool, error) {
	tag, err := q.pool.Exec(ctx, `
		INSERT INTO gh_deliveries (delivery_id) VALUES ($1)
		ON CONFLICT (delivery_id) DO NOTHING`, deliveryID)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (q *Queue) Enqueue(ctx context.Context, row Row) (int64, error) {
	var groupKey *string
	if row.GroupKey != "" {
		groupKey = &row.GroupKey
	}

	var id int64
	err := q.pool.QueryRow(ctx, `
		INSERT INTO outbox
			(chat_id, integration_id, event_kind, payload, group_key, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		row.ChatID, row.IntegrationID, row.Kind, []byte(row.Payload),
		groupKey, q.now().Add(row.Delay),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}
	return id, nil
}

// PruneDeliveries drops dedup records past their usefulness. GitHub gives up
// retrying long before this window, so anything older cannot cause a
// duplicate.
func (q *Queue) PruneDeliveries(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, `
		DELETE FROM gh_deliveries WHERE received_at < $1`, q.now().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("prune deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 4: Run and confirm they pass**

Run: `go test ./internal/outbox/`
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/outbox
git commit -m "feat: outbox enqueue with github delivery deduplication"
```

---

## Task 12: Outbox worker

**Files:**
- Create: `internal/outbox/worker.go`
- Test: `internal/outbox/worker_test.go`

**Interfaces:**
- Consumes: `outbox.Queue` (Task 11).
- Produces: interface `outbox.Deliverer` with `Deliver(ctx context.Context, job outbox.Job) error`; type `outbox.Job{ID int64, IntegrationID int64, TelegramChatID int64, TopicID *int64, Kind string, Payload json.RawMessage, Attempts int}`; `outbox.NewWorker(pool *pgxpool.Pool, d outbox.Deliverer, now func() time.Time) *outbox.Worker`; `(*Worker).RunOnce(ctx context.Context) (processed int, err error)`; `(*Worker).Run(ctx context.Context, interval time.Duration)`; sentinel `outbox.ErrPermanent` for failures that must not be retried; `outbox.Backoff(attempts int) time.Duration`.

- [ ] **Step 1: Write the failing test**

Create `internal/outbox/worker_test.go`:

```go
package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/outbox"
)

// recordingDeliverer captures the jobs handed to it and returns a scripted
// error, so the worker's retry decisions can be asserted directly.
type recordingDeliverer struct {
	mu   sync.Mutex
	jobs []outbox.Job
	err  error
}

func (d *recordingDeliverer) Deliver(_ context.Context, job outbox.Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jobs = append(d.jobs, job)
	return d.err
}

func (d *recordingDeliverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.jobs)
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	require.Equal(t, time.Second, outbox.Backoff(1))
	require.Equal(t, 5*time.Second, outbox.Backoff(2))
	require.Equal(t, 30*time.Second, outbox.Backoff(3))
	require.Equal(t, 2*time.Minute, outbox.Backoff(4))
	require.Equal(t, 10*time.Minute, outbox.Backoff(5))
	require.Equal(t, time.Hour, outbox.Backoff(6))
	require.Equal(t, time.Hour, outbox.Backoff(99))
}

func TestRunOnceDeliversAndMarksSent(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{"ref":"x"}`),
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, deliverer.count())
	require.Equal(t, int64(-100), deliverer.jobs[0].TelegramChatID)

	var status string
	var sentAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, sent_at FROM outbox WHERE id = $1`, id).Scan(&status, &sentAt))
	require.Equal(t, "sent", status)
	require.NotNil(t, sentAt)
}

func TestRunOnceIgnoresRowsScheduledInTheFuture(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	_, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "star", Payload: json.RawMessage(`{}`), Delay: time.Minute,
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Zero(t, deliverer.count())
}

func TestRunOnceReschedulesOnTransientFailure(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{err: errors.New("telegram timeout")}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var (
		status      string
		attempts    int
		scheduledAt time.Time
		lastError   *string
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts, scheduled_at, last_error FROM outbox WHERE id = $1`, id).
		Scan(&status, &attempts, &scheduledAt, &lastError))

	require.Equal(t, "pending", status)
	require.Equal(t, 1, attempts)
	require.Equal(t, now.Add(time.Second), scheduledAt.UTC())
	require.NotNil(t, lastError)
	require.Contains(t, *lastError, "telegram timeout")
}

func TestRunOnceFailsPermanentlyAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Pretend five attempts already failed; this run is the sixth.
	_, err = pool.Exec(ctx, `UPDATE outbox SET attempts = 5 WHERE id = $1`, id)
	require.NoError(t, err)

	deliverer := &recordingDeliverer{err: errors.New("still failing")}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, id).Scan(&status))
	require.Equal(t, "failed", status)
}

func TestRunOnceDoesNotRetryPermanentErrors(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Being kicked from a chat will never resolve itself by waiting.
	deliverer := &recordingDeliverer{
		err: errors.Join(outbox.ErrPermanent, errors.New("bot was kicked")),
	}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var status string
	var attempts int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts FROM outbox WHERE id = $1`, id).Scan(&status, &attempts))
	require.Equal(t, "failed", status)
	require.Equal(t, 1, attempts)
}

func TestConcurrentWorkersDoNotDeliverTheSameRowTwice(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	for range 20 {
		_, err := queue.Enqueue(ctx, outbox.Row{
			ChatID: chatID, IntegrationID: integrationID,
			Kind: "push", Payload: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	deliverer := &recordingDeliverer{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
			for {
				n, err := worker.RunOnce(ctx)
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 20, deliverer.count(), "each row must be delivered exactly once")
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/outbox/ -run 'TestBackoff|TestRunOnce|TestConcurrent'`
Expected: FAIL — `outbox.NewWorker` is undefined.

- [ ] **Step 3: Implement the worker**

Create `internal/outbox/worker.go`:

```go
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPermanent marks a failure that waiting cannot fix — the bot was kicked,
// the chat is gone, the payload is unrenderable. Wrap it to skip retries.
var ErrPermanent = errors.New("permanent delivery failure")

// maxAttempts is the number of tries before a row is abandoned. With the
// backoff schedule below this spans roughly 13 minutes.
const maxAttempts = 6

// batchSize bounds how many rows one RunOnce claims, keeping a single
// transaction short even when a backlog has built up.
const batchSize = 20

type Job struct {
	ID             int64
	IntegrationID  int64
	TelegramChatID int64
	TopicID        *int64
	Kind           string
	Payload        json.RawMessage
	Attempts       int
}

type Deliverer interface {
	Deliver(ctx context.Context, job Job) error
}

type Worker struct {
	pool      *pgxpool.Pool
	deliverer Deliverer
	now       func() time.Time
}

func NewWorker(pool *pgxpool.Pool, d Deliverer, now func() time.Time) *Worker {
	return &Worker{pool: pool, deliverer: d, now: now}
}

// Backoff maps an attempt number to the wait before the next try.
func Backoff(attempts int) time.Duration {
	schedule := []time.Duration{
		time.Second, 5 * time.Second, 30 * time.Second,
		2 * time.Minute, 10 * time.Minute, time.Hour,
	}
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempts-1]
}

// Run drains the queue on a ticker until the context is cancelled.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				n, err := w.RunOnce(ctx)
				if err != nil {
					slog.Error("outbox run failed", "error", err)
					break
				}
				// Keep going while the queue still has work, so a backlog
				// drains without waiting a full tick per batch.
				if n < batchSize {
					break
				}
			}
		}
	}
}

// RunOnce claims a batch, delivers each job, and records the outcome. Claiming
// happens in its own short transaction; delivery happens outside it so a slow
// Telegram call never holds row locks.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	jobs, err := w.claim(ctx)
	if err != nil {
		return 0, err
	}

	for _, job := range jobs {
		deliverErr := w.deliverer.Deliver(ctx, job)
		if err := w.record(ctx, job, deliverErr); err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

// claim marks rows as in-flight. SKIP LOCKED is what lets several workers
// share one queue without coordinating: each grabs rows nobody else holds.
func (w *Worker) claim(ctx context.Context) ([]Job, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH claimed AS (
			SELECT o.id
			FROM outbox o
			WHERE o.status = 'pending' AND o.scheduled_at <= $1
			ORDER BY o.scheduled_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox o
		SET status = 'sending'
		FROM claimed, chats c
		WHERE o.id = claimed.id AND c.id = o.chat_id
		RETURNING o.id, o.integration_id, c.telegram_chat_id, c.topic_id,
		          o.event_kind, o.payload, o.attempts`,
		w.now(), batchSize)
	if err != nil {
		return nil, fmt.Errorf("claim rows: %w", err)
	}

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(
			&job.ID, &job.IntegrationID, &job.TelegramChatID, &job.TopicID,
			&job.Kind, &job.Payload, &job.Attempts,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return jobs, nil
}

func (w *Worker) record(ctx context.Context, job Job, deliverErr error) error {
	if deliverErr == nil {
		_, err := w.pool.Exec(ctx, `
			UPDATE outbox SET status = 'sent', sent_at = $2, attempts = attempts + 1
			WHERE id = $1`, job.ID, w.now())
		if err != nil {
			return fmt.Errorf("mark sent: %w", err)
		}
		// last_event_at powers the health screen and is only meaningful for
		// messages that actually arrived.
		_, err = w.pool.Exec(ctx, `
			UPDATE integrations SET last_event_at = $2 WHERE id = $1`,
			job.IntegrationID, w.now())
		if err != nil {
			return fmt.Errorf("touch integration: %w", err)
		}
		return nil
	}

	attempts := job.Attempts + 1
	permanent := errors.Is(deliverErr, ErrPermanent) || attempts >= maxAttempts

	if permanent {
		_, err := w.pool.Exec(ctx, `
			UPDATE outbox SET status = 'failed', attempts = $2, last_error = $3
			WHERE id = $1`, job.ID, attempts, deliverErr.Error())
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		return nil
	}

	_, err := w.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'pending', attempts = $2, last_error = $3, scheduled_at = $4
		WHERE id = $1`,
		job.ID, attempts, deliverErr.Error(), w.now().Add(Backoff(attempts)))
	if err != nil {
		return fmt.Errorf("reschedule: %w", err)
	}
	return nil
}

// ensure pgx is referenced even if a future edit drops its only use.
var _ = pgx.ErrNoRows
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/outbox/...`
Expected: PASS, twelve tests. The concurrency test is the important one — if
it reports more than 20 deliveries, the `SKIP LOCKED` claim is wrong.

- [ ] **Step 5: Commit**

```bash
git add internal/outbox
git commit -m "feat: outbox worker with skip-locked claim and backoff retry"
```

---

## Task 13: Telegram sender

**Files:**
- Create: `internal/tg/sender.go`
- Test: `internal/tg/sender_test.go`

**Interfaces:**
- Consumes: `events.Render` (Task 8), `outbox.Job` and `outbox.ErrPermanent` (Task 12), `render.Strip` (Task 7).
- Produces: interface `tg.API` with `SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)`; `tg.NewSender(api tg.API, onTopicMissing func(ctx context.Context, chatID int64) error) *tg.Sender`; `(*Sender).Deliver(ctx context.Context, job outbox.Job) error`; `tg.Split(html string, limit int) []string`; `tg.ClassifyError(err error) (permanent bool, retryAfter time.Duration)`.

- [ ] **Step 1: Add telego**

```bash
go get github.com/mymmrac/telego@v1.11.1
```

- [ ] **Step 2: Write the failing test**

Create `internal/tg/sender_test.go`:

```go
package tg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	_ "github.com/faustyu/gh-notify-go/internal/events" // register event kinds
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/tg"
)

// fakeAPI records every SendMessage call and replays a scripted error queue.
type fakeAPI struct {
	mu     sync.Mutex
	sent   []*telego.SendMessageParams
	errors []error
}

func (f *fakeAPI) SendMessage(
	_ context.Context, params *telego.SendMessageParams,
) (*telego.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sent = append(f.sent, params)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return nil, err
	}
	return &telego.Message{MessageID: len(f.sent)}, nil
}

func pushJob(t *testing.T) outbox.Job {
	t.Helper()
	return outbox.Job{
		ID: 1, IntegrationID: 1, TelegramChatID: -100, Kind: "push",
		Payload: json.RawMessage(`{
			"ref":"refs/heads/main",
			"repository":{"full_name":"acme/app"},
			"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
			"commits":[{"id":"bbbbbbbb","message":"fix: x","url":"https://x/1"}]
		}`),
	}
}

func TestDeliverSendsRenderedHTML(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	require.NoError(t, sender.Deliver(context.Background(), pushJob(t)))
	require.Len(t, api.sent, 1)

	sent := api.sent[0]
	require.Equal(t, telego.ModeHTML, sent.ParseMode)
	require.Contains(t, sent.Text, "acme/app")
	require.Contains(t, sent.Text, "tg-emoji")
	require.True(t, sent.LinkPreviewOptions.IsDisabled)
}

func TestDeliverRoutesToForumTopic(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	topic := int64(77)
	job := pushJob(t)
	job.TopicID = &topic

	require.NoError(t, sender.Deliver(context.Background(), job))
	require.Equal(t, 77, api.sent[0].MessageThreadID)
}

func TestDeliverRetriesWithoutTopicWhenTopicIsGone(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message thread not found",
	}}}

	var clearedChat int64
	sender := tg.NewSender(api, func(_ context.Context, chatID int64) error {
		clearedChat = chatID
		return nil
	})

	topic := int64(77)
	job := pushJob(t)
	job.TopicID = &topic

	require.NoError(t, sender.Deliver(context.Background(), job))
	require.Len(t, api.sent, 2)
	require.Equal(t, 77, api.sent[0].MessageThreadID)
	require.Zero(t, api.sent[1].MessageThreadID, "retry must drop the thread id")
	require.Equal(t, int64(-100), clearedChat, "the dead topic must be cleared")
}

func TestDeliverRetriesWithoutCustomEmojiWhenRejected(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: can't parse entities: unsupported custom emoji",
	}}}
	sender := tg.NewSender(api, nil)

	require.NoError(t, sender.Deliver(context.Background(), pushJob(t)))
	require.Len(t, api.sent, 2)
	require.Contains(t, api.sent[0].Text, "tg-emoji")
	require.NotContains(t, api.sent[1].Text, "tg-emoji")
	require.Contains(t, api.sent[1].Text, "⬆", "the unicode fallback must survive")
}

func TestDeliverReportsKickedAsPermanent(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   403,
		Description: "Forbidden: bot was kicked from the supergroup chat",
	}}}
	sender := tg.NewSender(api, nil)

	err := sender.Deliver(context.Background(), pushJob(t))
	require.ErrorIs(t, err, outbox.ErrPermanent)
}

func TestDeliverTreatsUnknownEventAsPermanent(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	job := pushJob(t)
	job.Kind = "not_a_real_event"

	err := sender.Deliver(context.Background(), job)
	require.ErrorIs(t, err, outbox.ErrPermanent)
	require.Empty(t, api.sent)
}

func TestDeliverSplitsOverlongMessages(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	commits := make([]string, 0, 400)
	for i := range 400 {
		commits = append(commits, `{"id":"aaaaaaaa","message":"commit `+
			strings.Repeat("x", 40)+string(rune('a'+i%26))+`","url":"https://x/1"}`)
	}
	job := pushJob(t)
	job.Payload = json.RawMessage(`{
		"ref":"refs/heads/main",
		"repository":{"full_name":"acme/app"},
		"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
		"commits":[` + strings.Join(commits, ",") + `]}`)

	require.NoError(t, sender.Deliver(context.Background(), job))
	for _, sent := range api.sent {
		require.LessOrEqual(t, len([]rune(sent.Text)), 4096)
	}
}

func TestSplitBreaksOnLineBoundaries(t *testing.T) {
	body := strings.Repeat("line of text\n", 500)
	parts := tg.Split(body, 200)

	require.Greater(t, len(parts), 1)
	for _, p := range parts {
		require.LessOrEqual(t, len([]rune(p)), 200)
	}
	require.Equal(t, strings.TrimSpace(body), strings.TrimSpace(strings.Join(parts, "\n")))
}

func TestSplitKeepsShortTextIntact(t *testing.T) {
	require.Equal(t, []string{"short"}, tg.Split("short", 4096))
}

func TestClassifyErrorReadsRetryAfter(t *testing.T) {
	permanent, retryAfter := tg.ClassifyError(&telegoapi.Error{
		ErrorCode:  429,
		Parameters: &telegoapi.ResponseParameters{RetryAfter: 12},
	})
	require.False(t, permanent)
	require.Equal(t, 12*time.Second, retryAfter)
}

func TestClassifyErrorTreatsNetworkErrorsAsTransient(t *testing.T) {
	permanent, retryAfter := tg.ClassifyError(errors.New("connection reset"))
	require.False(t, permanent)
	require.Zero(t, retryAfter)
}
```

- [ ] **Step 3: Run and confirm it fails**

Run: `go test ./internal/tg/`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement the sender**

Create `internal/tg/sender.go`:

```go
// Package tg turns outbox jobs into Telegram messages. It owns every
// Telegram-specific failure mode so the worker only has to know "retry" or
// "give up".
package tg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/outbox"
)

// messageLimit is Telegram's hard cap on message length in characters.
const messageLimit = 4096

type API interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
}

type Sender struct {
	api API

	// onTopicMissing clears a forum topic that Telegram says no longer
	// exists, so later events go to the General topic instead of failing.
	onTopicMissing func(ctx context.Context, chatID int64) error
}

func NewSender(api API, onTopicMissing func(ctx context.Context, chatID int64) error) *Sender {
	return &Sender{api: api, onTopicMissing: onTopicMissing}
}

func (s *Sender) Deliver(ctx context.Context, job outbox.Job) error {
	html, err := events.Render(events.Kind(job.Kind), job.Payload)
	if err != nil {
		// An unrenderable payload will not become renderable later.
		return fmt.Errorf("%w: render %s: %v", outbox.ErrPermanent, job.Kind, err)
	}

	for _, part := range Split(html, messageLimit) {
		if err := s.sendPart(ctx, job, part); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) sendPart(ctx context.Context, job outbox.Job, text string) error {
	params := &telego.SendMessageParams{
		ChatID:             telego.ChatID{ID: job.TelegramChatID},
		Text:               text,
		ParseMode:          telego.ModeHTML,
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	}
	if job.TopicID != nil {
		params.MessageThreadID = int(*job.TopicID)
	}

	_, err := s.api.SendMessage(ctx, params)
	if err == nil {
		return nil
	}

	// The topic was deleted. Clear it and resend to the General topic.
	if isTopicMissing(err) && job.TopicID != nil {
		if s.onTopicMissing != nil {
			if clearErr := s.onTopicMissing(ctx, job.TelegramChatID); clearErr != nil {
				return fmt.Errorf("clear missing topic: %w", clearErr)
			}
		}
		params.MessageThreadID = 0
		if _, retryErr := s.api.SendMessage(ctx, params); retryErr != nil {
			return classify(retryErr)
		}
		return nil
	}

	// Custom emoji were rejected. Resend once with the tags stripped, so the
	// message still arrives looking plain rather than not arriving at all.
	if isCustomEmojiRejected(err) {
		params.Text = render.Strip(params.Text)
		if _, retryErr := s.api.SendMessage(ctx, params); retryErr != nil {
			return classify(retryErr)
		}
		return nil
	}

	return classify(err)
}

func classify(err error) error {
	permanent, retryAfter := ClassifyError(err)
	if permanent {
		return fmt.Errorf("%w: %v", outbox.ErrPermanent, err)
	}
	if retryAfter > 0 {
		// Sleeping here is deliberate and bounded: Telegram told us exactly
		// how long to wait, and the worker's own backoff is coarser.
		time.Sleep(retryAfter)
	}
	return err
}

// ClassifyError decides whether waiting could ever help, and how long
// Telegram asked us to wait.
func ClassifyError(err error) (permanent bool, retryAfter time.Duration) {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		// Network-level failures are transient by nature.
		return false, 0
	}

	if apiErr.Parameters != nil && apiErr.Parameters.RetryAfter > 0 {
		return false, time.Duration(apiErr.Parameters.RetryAfter) * time.Second
	}

	switch apiErr.ErrorCode {
	case 403: // kicked, blocked, or no longer a member
		return true, 0
	case 400:
		desc := strings.ToLower(apiErr.Description)
		// "chat not found" and friends never recover; a malformed request
		// will not become well-formed on retry either.
		return strings.Contains(desc, "chat not found") ||
			strings.Contains(desc, "group chat was upgraded") ||
			strings.Contains(desc, "can't parse entities"), 0
	default:
		return false, 0
	}
}

func isTopicMissing(err error) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	desc := strings.ToLower(apiErr.Description)
	return strings.Contains(desc, "message thread not found") ||
		strings.Contains(desc, "topic_deleted") ||
		strings.Contains(desc, "topic was deleted")
}

func isCustomEmojiRejected(err error) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	desc := strings.ToLower(apiErr.Description)
	return strings.Contains(desc, "custom emoji")
}

// Split breaks a long message on line boundaries. Splitting mid-tag would
// produce invalid HTML, and Telegram rejects the whole message when that
// happens.
func Split(html string, limit int) []string {
	if len([]rune(html)) <= limit {
		return []string{html}
	}

	var (
		parts   []string
		current strings.Builder
	)
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, strings.TrimRight(current.String(), "\n"))
			current.Reset()
		}
	}

	for _, line := range strings.Split(html, "\n") {
		// A single line longer than the limit is cut on a rune boundary;
		// this only happens with pathological input.
		for len([]rune(line)) > limit {
			runes := []rune(line)
			flush()
			parts = append(parts, string(runes[:limit]))
			line = string(runes[limit:])
		}
		if len([]rune(current.String()))+len([]rune(line))+1 > limit {
			flush()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return parts
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/tg/`
Expected: PASS, eleven tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tg go.mod go.sum
git commit -m "feat: telegram sender with splitting, topic fallback and error classification"
```

---

## Task 14: Ingest service and the webhook endpoint

**Files:**
- Create: `internal/service/ingest.go`
- Create: `internal/httpapi/webhook.go`
- Test: `internal/service/ingest_test.go`
- Test: `internal/httpapi/webhook_test.go`

**Interfaces:**
- Consumes: `ghapp.ParseEnvelope`, `ghapp.VerifySignature` (Task 4), `storage.Store` (Task 10), `outbox.Queue` (Task 11), `events.Wanted` (Task 8).
- Produces: `service.NewIngest(store *storage.Store, queue *outbox.Queue) *service.Ingest`; `(*Ingest).Handle(ctx context.Context, env ghapp.Envelope) (service.Result, error)`; type `service.Result{Matched, Enqueued, Skipped int, Duplicate bool}`; `httpapi.NewWebhookHandler(secret string, ingest *service.Ingest) http.Handler`.

- [ ] **Step 1: Write the failing ingest test**

Create `internal/service/ingest_test.go`:

```go
package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	_ "github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func newIngest(t *testing.T) (*service.Ingest, *pgxpool.Pool, int64) {
	t.Helper()
	ctx := context.Background()

	url := testhelper.StartPostgres(t)
	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)

	store, err := storage.New(ctx, url, box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	userID, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)
	integrationID, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	queue := outbox.NewQueue(store.Pool(), time.Now)
	return service.NewIngest(store, queue), store.Pool(), integrationID
}

func envelope(kind, action, delivery string, body string) ghapp.Envelope {
	env, _ := ghapp.ParseEnvelope(kind, delivery, []byte(body))
	env.Action = action
	return env
}

const pushBody = `{
	"ref":"refs/heads/main",
	"repository":{"id":42,"full_name":"acme/app"},
	"installation":{"id":7},
	"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
	"commits":[]
}`

func TestHandleEnqueuesForMatchingIntegration(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Enqueued)
	require.False(t, result.Duplicate)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_kind = 'push'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestHandleIgnoresRepeatedDelivery(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.True(t, result.Duplicate)
	require.Zero(t, result.Enqueued)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestHandleSkipsDisabledEventKind(t *testing.T) {
	ctx := context.Background()
	ingest, pool, integrationID := newIngest(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO event_settings (integration_id, event_kind, enabled)
		 VALUES ($1, 'push', false)`, integrationID)
	require.NoError(t, err)

	result, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)
	require.Equal(t, 1, result.Matched)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, result.Enqueued)
}

func TestHandleSkipsUnwantedAction(t *testing.T) {
	ctx := context.Background()
	ingest, _, _ := newIngest(t)

	body := `{"action":"labeled","repository":{"id":42,"full_name":"acme/app"},
	          "installation":{"id":7},"pull_request":{}}`
	result, err := ingest.Handle(ctx, envelope("pull_request", "labeled", "d-2", body))
	require.NoError(t, err)
	require.Zero(t, result.Enqueued)
	require.Zero(t, result.Matched, "an unwanted action must not even query integrations")
}

func TestHandleWithNoIntegrationsIsQuiet(t *testing.T) {
	ctx := context.Background()
	ingest, _, _ := newIngest(t)

	body := `{"repository":{"id":999,"full_name":"other/repo"},
	          "installation":{"id":7},"commits":[]}`
	result, err := ingest.Handle(ctx, envelope("push", "", "d-3", body))
	require.NoError(t, err)
	require.Zero(t, result.Matched)
	require.Zero(t, result.Enqueued)
}

func TestHandleStoresFullPayload(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, envelope("push", "", "d-1", pushBody))
	require.NoError(t, err)

	var payload []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT payload FROM outbox`).Scan(&payload))

	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.Contains(t, parsed, "repository")
	require.Contains(t, parsed, "sender")
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/service/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the ingest service**

Create `internal/service/ingest.go`:

```go
// Package service holds application logic that sits between transport and
// storage.
package service

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// Result is returned to the webhook endpoint and logged. It exists so an
// operator can tell "nobody subscribed" apart from "we dropped it".
type Result struct {
	Matched   int
	Enqueued  int
	Skipped   int
	Duplicate bool
}

type Ingest struct {
	store *storage.Store
	queue *outbox.Queue
}

func NewIngest(store *storage.Store, queue *outbox.Queue) *Ingest {
	return &Ingest{store: store, queue: queue}
}

// Handle fans one webhook out to every subscribed chat. It only writes to the
// database — no Telegram call happens on this path, so GitHub gets its 200
// regardless of Telegram's health.
func (i *Ingest) Handle(ctx context.Context, env ghapp.Envelope) (Result, error) {
	var result Result

	// Checked before any database work: most deliveries are actions nobody
	// wants, and this keeps them from costing a query.
	if !events.Wanted(events.Kind(env.Kind), env.Action) {
		return result, nil
	}

	fresh, err := i.queue.MarkDelivered(ctx, env.DeliveryID)
	if err != nil {
		return result, fmt.Errorf("dedup delivery: %w", err)
	}
	if !fresh {
		result.Duplicate = true
		return result, nil
	}

	integrations, err := i.store.IntegrationsForRepo(ctx, env.RepoGitHubID, env.InstallationID)
	if err != nil {
		return result, fmt.Errorf("find integrations: %w", err)
	}
	result.Matched = len(integrations)

	for _, integration := range integrations {
		enabled, err := i.store.EventEnabled(ctx, integration.ID, env.Kind)
		if err != nil {
			return result, fmt.Errorf("check event setting: %w", err)
		}
		if !enabled {
			result.Skipped++
			continue
		}

		if _, err := i.queue.Enqueue(ctx, outbox.Row{
			ChatID:        integration.ChatID,
			IntegrationID: integration.ID,
			Kind:          env.Kind,
			Payload:       env.Raw,
		}); err != nil {
			return result, fmt.Errorf("enqueue for integration %d: %w", integration.ID, err)
		}
		result.Enqueued++
	}
	return result, nil
}
```

- [ ] **Step 4: Write the failing HTTP test**

Create `internal/httpapi/webhook_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	body := []byte(`{"zen":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/gh/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signBody("wrong", body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "d-1")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhookRejectsMissingHeaders(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/gh/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signBody("s3cret", body))
	// No X-GitHub-Event header.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhookRejectsNonPost(t *testing.T) {
	handler := httpapi.NewWebhookHandler("s3cret", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gh/webhook", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
```

Note: these three cases all reject before the ingest service is reached, so
passing `nil` is safe and keeps the HTTP test free of a database.

- [ ] **Step 5: Run and confirm it fails**

Run: `go test ./internal/httpapi/`
Expected: FAIL — package does not exist.

- [ ] **Step 6: Implement the webhook handler**

Create `internal/httpapi/webhook.go`:

```go
// Package httpapi exposes the GitHub-facing HTTP surface.
package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/service"
)

// maxBodyBytes bounds what we will read from an unauthenticated request.
// GitHub payloads are well under this.
const maxBodyBytes = 8 << 20

func NewWebhookHandler(secret string, ingest *service.Ingest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		// Signature first: nothing else touches unauthenticated input.
		if err := ghapp.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}

		kind := r.Header.Get("X-GitHub-Event")
		delivery := r.Header.Get("X-GitHub-Delivery")
		if kind == "" || delivery == "" {
			http.Error(w, "missing github headers", http.StatusBadRequest)
			return
		}

		env, err := ghapp.ParseEnvelope(kind, delivery, body)
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		result, err := ingest.Handle(r.Context(), env)
		if err != nil {
			// 500 makes GitHub retry, which is what we want: the delivery
			// is not lost, it is redelivered once we are healthy again.
			slog.Error("ingest failed", "delivery", delivery, "kind", kind, "error", err)
			http.Error(w, "ingest failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
```

- [ ] **Step 7: Run both packages and confirm they pass**

Run: `go test ./internal/service/ ./internal/httpapi/`
Expected: PASS, nine tests.

- [ ] **Step 8: Commit**

```bash
git add internal/service internal/httpapi
git commit -m "feat: ingest service and github webhook endpoint"
```

---

## Task 15: Screen engine

**Files:**
- Create: `internal/tg/ui/engine.go`
- Create: `internal/tg/ui/navstore.go`
- Test: `internal/tg/ui/engine_test.go`

**Interfaces:**
- Consumes: `pgxpool.Pool` from Task 10.
- Produces:
  - `ui.Params map[string]string`
  - `ui.Session{UserID int64, TelegramID int64, Params ui.Params, Depth int}`
  - `ui.Button{Label string, Screen string, Params ui.Params, URL string}`
  - `ui.View{Text string, Rows [][]ui.Button}`
  - interface `ui.Screen` with `Name() string` and `Render(ctx context.Context, s ui.Session) (ui.View, error)`
  - `ui.NewEngine(nav ui.NavStore) *ui.Engine`; `(*Engine).Register(screens ...ui.Screen)`
  - `(*Engine).Open(ctx context.Context, userID, telegramID int64, screen string, params ui.Params) (ui.View, error)`
  - `(*Engine).Back(ctx context.Context, userID, telegramID int64) (ui.View, error)`
  - `(*Engine).Resolve(ctx context.Context, userID int64, key string) (screen string, params ui.Params, err error)`
  - `(*Engine).ActionKey(ctx context.Context, userID int64, screen string, params ui.Params) (string, error)`
  - interface `ui.NavStore` with `Push`, `Pop`, `Depth`, `PutAction`, `GetAction`, `AnchorMessageID`, `SetAnchorMessageID`
  - `ui.NewPostgresNav(pool *pgxpool.Pool) ui.NavStore`
  - `ui.BackButtonLabel = "◁ Назад"`

- [ ] **Step 1: Write the failing test**

Create `internal/tg/ui/engine_test.go`:

```go
package ui_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// stubScreen renders its own name plus whatever params it received, which is
// enough to assert routing without pulling in real screens.
type stubScreen struct {
	name string
	rows [][]ui.Button
}

func (s stubScreen) Name() string { return s.name }

func (s stubScreen) Render(_ context.Context, sess ui.Session) (ui.View, error) {
	return ui.View{Text: s.name + ":" + sess.Params["id"], Rows: s.rows}, nil
}

func newEngine(t *testing.T) (*ui.Engine, *pgxpool.Pool, int64) {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (555) RETURNING id`).Scan(&userID))

	engine := ui.NewEngine(ui.NewPostgresNav(pool))
	engine.Register(
		stubScreen{name: "home", rows: [][]ui.Button{{
			{Label: "Репозитории", Screen: "repos"},
		}}},
		stubScreen{name: "repos"},
		stubScreen{name: "repo_detail"},
	)
	return engine, pool, userID
}

func TestOpenRendersScreenAndPushesStack(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	view, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestHomeHasNoBackButton(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	view, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	for _, row := range view.Rows {
		for _, b := range row {
			require.NotEqual(t, ui.BackButtonLabel, b.Label)
		}
	}
}

func TestDeeperScreensGetBackButton(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	view, err := engine.Open(ctx, userID, 555, "repos", ui.Params{"id": "7"})
	require.NoError(t, err)
	require.Equal(t, "repos:7", view.Text)

	last := view.Rows[len(view.Rows)-1]
	require.Equal(t, ui.BackButtonLabel, last[0].Label)
}

func TestBackReturnsToPreviousScreenWithItsParams(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)
	_, err = engine.Open(ctx, userID, 555, "repos", ui.Params{"id": "7"})
	require.NoError(t, err)
	_, err = engine.Open(ctx, userID, 555, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	view, err := engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "repos:7", view.Text)

	view, err = engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestBackAtHomeStaysAtHome(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	view, err := engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestActionKeyIsShortEnoughForCallbackData(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	key, err := engine.ActionKey(ctx, userID, "repo_detail",
		ui.Params{"id": "a-very-long-repository-identifier-that-would-never-fit"})
	require.NoError(t, err)
	// Telegram caps callback_data at 64 bytes.
	require.LessOrEqual(t, len(key), 64)
}

func TestResolveReturnsStoredScreenAndParams(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	key, err := engine.ActionKey(ctx, userID, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	screen, params, err := engine.Resolve(ctx, userID, key)
	require.NoError(t, err)
	require.Equal(t, "repo_detail", screen)
	require.Equal(t, "99", params["id"])
}

func TestResolveRejectsAnotherUsersKey(t *testing.T) {
	ctx := context.Background()
	engine, pool, userID := newEngine(t)

	var otherID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (999) RETURNING id`).Scan(&otherID))

	key, err := engine.ActionKey(ctx, userID, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	_, _, err = engine.Resolve(ctx, otherID, key)
	require.ErrorIs(t, err, ui.ErrActionNotFound)
}

func TestOpenUnknownScreenIsAnError(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "nope", nil)
	require.ErrorIs(t, err, ui.ErrUnknownScreen)
}

func TestAnchorMessageIDRoundTrips(t *testing.T) {
	ctx := context.Background()
	_, pool, userID := newEngine(t)
	nav := ui.NewPostgresNav(pool)

	id, err := nav.AnchorMessageID(ctx, userID)
	require.NoError(t, err)
	require.Zero(t, id)

	require.NoError(t, nav.SetAnchorMessageID(ctx, userID, 4242))
	id, err = nav.AnchorMessageID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 4242, id)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/tg/ui/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the navigation store**

Create `internal/tg/ui/navstore.go`:

```go
package ui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrActionNotFound = errors.New("ui action not found")

// frame is one entry on the navigation stack.
type frame struct {
	Screen string `json:"s"`
	Params Params `json:"p,omitempty"`
}

// NavStore persists per-user navigation state. It lives in the database so
// ◁ Назад keeps working across a bot restart.
type NavStore interface {
	Push(ctx context.Context, userID int64, screen string, params Params) error
	Pop(ctx context.Context, userID int64) (screen string, params Params, err error)
	Depth(ctx context.Context, userID int64) (int, error)
	PutAction(ctx context.Context, userID int64, key, screen string, params Params) error
	GetAction(ctx context.Context, userID int64, key string) (screen string, params Params, err error)
	AnchorMessageID(ctx context.Context, userID int64) (int, error)
	SetAnchorMessageID(ctx context.Context, userID int64, messageID int) error
}

type postgresNav struct{ pool *pgxpool.Pool }

func NewPostgresNav(pool *pgxpool.Pool) NavStore { return postgresNav{pool: pool} }

func (n postgresNav) load(ctx context.Context, userID int64) ([]frame, error) {
	var raw []byte
	err := n.pool.QueryRow(ctx,
		`SELECT stack FROM ui_nav WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load nav stack: %w", err)
	}

	var stack []frame
	if err := json.Unmarshal(raw, &stack); err != nil {
		return nil, fmt.Errorf("decode nav stack: %w", err)
	}
	return stack, nil
}

func (n postgresNav) save(ctx context.Context, userID int64, stack []frame) error {
	raw, err := json.Marshal(stack)
	if err != nil {
		return fmt.Errorf("encode nav stack: %w", err)
	}
	_, err = n.pool.Exec(ctx, `
		INSERT INTO ui_nav (user_id, stack, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET stack = EXCLUDED.stack, updated_at = now()`,
		userID, raw)
	if err != nil {
		return fmt.Errorf("save nav stack: %w", err)
	}
	return nil
}

// Push replaces the whole stack when the target is "home", so returning to
// the root cannot leave an ever-growing history behind.
func (n postgresNav) Push(ctx context.Context, userID int64, screen string, params Params) error {
	if screen == "home" {
		return n.save(ctx, userID, []frame{{Screen: screen, Params: params}})
	}

	stack, err := n.load(ctx, userID)
	if err != nil {
		return err
	}
	return n.save(ctx, userID, append(stack, frame{Screen: screen, Params: params}))
}

// Pop removes the current frame and returns the one beneath it. At the root
// it returns the root again, so ◁ Назад is never a dead end.
func (n postgresNav) Pop(ctx context.Context, userID int64) (string, Params, error) {
	stack, err := n.load(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	if len(stack) == 0 {
		return "home", nil, nil
	}
	if len(stack) == 1 {
		return stack[0].Screen, stack[0].Params, nil
	}

	stack = stack[:len(stack)-1]
	if err := n.save(ctx, userID, stack); err != nil {
		return "", nil, err
	}
	top := stack[len(stack)-1]
	return top.Screen, top.Params, nil
}

func (n postgresNav) Depth(ctx context.Context, userID int64) (int, error) {
	stack, err := n.load(ctx, userID)
	if err != nil {
		return 0, err
	}
	return len(stack), nil
}

func (n postgresNav) PutAction(
	ctx context.Context, userID int64, key, screen string, params Params,
) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode action params: %w", err)
	}
	_, err = n.pool.Exec(ctx, `
		INSERT INTO ui_actions (key, user_id, screen, params) VALUES ($1, $2, $3, $4)`,
		key, userID, screen, raw)
	if err != nil {
		return fmt.Errorf("store action: %w", err)
	}
	return nil
}

// GetAction scopes the lookup to the user, so a callback key leaked from one
// chat cannot drive another user's session.
func (n postgresNav) GetAction(
	ctx context.Context, userID int64, key string,
) (string, Params, error) {
	var (
		screen string
		raw    []byte
	)
	err := n.pool.QueryRow(ctx, `
		SELECT screen, params FROM ui_actions WHERE key = $1 AND user_id = $2`,
		key, userID).Scan(&screen, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrActionNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("load action: %w", err)
	}

	var params Params
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", nil, fmt.Errorf("decode action params: %w", err)
	}
	return screen, params, nil
}

func (n postgresNav) AnchorMessageID(ctx context.Context, userID int64) (int, error) {
	var messageID *int64
	err := n.pool.QueryRow(ctx,
		`SELECT message_id FROM ui_nav WHERE user_id = $1`, userID).Scan(&messageID)
	if errors.Is(err, pgx.ErrNoRows) || messageID == nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load anchor message: %w", err)
	}
	return int(*messageID), nil
}

func (n postgresNav) SetAnchorMessageID(ctx context.Context, userID int64, messageID int) error {
	_, err := n.pool.Exec(ctx, `
		INSERT INTO ui_nav (user_id, message_id) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET message_id = EXCLUDED.message_id`,
		userID, messageID)
	if err != nil {
		return fmt.Errorf("save anchor message: %w", err)
	}
	return nil
}

// newActionKey produces a short opaque token. 12 random bytes base64url is 16
// characters, comfortably inside Telegram's 64-byte callback_data limit no
// matter how large the params are.
func newActionKey() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate action key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
```

- [ ] **Step 4: Implement the engine**

Create `internal/tg/ui/engine.go`:

```go
// Package ui is the screen engine. A screen is a pure function of session
// state; the engine owns navigation, callback indirection, and the back
// button, so individual screens never deal with any of it.
package ui

import (
	"context"
	"errors"
	"fmt"
)

var ErrUnknownScreen = errors.New("unknown screen")

// BackButtonLabel is plain Unicode: Telegram button labels cannot carry
// message entities, so premium emoji are impossible here by design.
const BackButtonLabel = "◁ Назад"

type Params map[string]string

type Session struct {
	UserID     int64
	TelegramID int64
	Params     Params
	Depth      int
}

// Button is either a navigation target (Screen set) or an external link
// (URL set).
type Button struct {
	Label  string
	Screen string
	Params Params
	URL    string
}

type View struct {
	Text string
	Rows [][]Button
}

type Screen interface {
	Name() string
	Render(ctx context.Context, s Session) (View, error)
}

type Engine struct {
	nav     NavStore
	screens map[string]Screen
}

func NewEngine(nav NavStore) *Engine {
	return &Engine{nav: nav, screens: map[string]Screen{}}
}

func (e *Engine) Register(screens ...Screen) {
	for _, s := range screens {
		e.screens[s.Name()] = s
	}
}

// Open renders a screen and records it on the navigation stack.
func (e *Engine) Open(
	ctx context.Context, userID, telegramID int64, screen string, params Params,
) (View, error) {
	if _, ok := e.screens[screen]; !ok {
		return View{}, fmt.Errorf("%w: %s", ErrUnknownScreen, screen)
	}
	if err := e.nav.Push(ctx, userID, screen, params); err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params)
}

// Back pops one frame and renders what is underneath.
func (e *Engine) Back(ctx context.Context, userID, telegramID int64) (View, error) {
	screen, params, err := e.nav.Pop(ctx, userID)
	if err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params)
}

func (e *Engine) render(
	ctx context.Context, userID, telegramID int64, screen string, params Params,
) (View, error) {
	impl, ok := e.screens[screen]
	if !ok {
		return View{}, fmt.Errorf("%w: %s", ErrUnknownScreen, screen)
	}

	depth, err := e.nav.Depth(ctx, userID)
	if err != nil {
		return View{}, err
	}

	view, err := impl.Render(ctx, Session{
		UserID: userID, TelegramID: telegramID, Params: params, Depth: depth,
	})
	if err != nil {
		return View{}, fmt.Errorf("render %s: %w", screen, err)
	}

	// The engine appends the back button rather than each screen doing it,
	// which is what guarantees it is present everywhere below the root.
	if depth > 1 {
		view.Rows = append(view.Rows, []Button{{Label: BackButtonLabel, Screen: backScreen}})
	}
	return view, nil
}

// backScreen is a reserved target the callback handler translates into a Back
// call rather than an Open.
const backScreen = "__back"

// IsBack reports whether a resolved screen name means "go back".
func IsBack(screen string) bool { return screen == backScreen }

// ActionKey stores a button's target and returns the opaque token to put in
// callback_data.
func (e *Engine) ActionKey(
	ctx context.Context, userID int64, screen string, params Params,
) (string, error) {
	key, err := newActionKey()
	if err != nil {
		return "", err
	}
	if err := e.nav.PutAction(ctx, userID, key, screen, params); err != nil {
		return "", err
	}
	return key, nil
}

func (e *Engine) Resolve(
	ctx context.Context, userID int64, key string,
) (string, Params, error) {
	return e.nav.GetAction(ctx, userID, key)
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/tg/ui/`
Expected: PASS, ten tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tg/ui
git commit -m "feat: screen engine with persistent nav stack and callback indirection"
```

---

## Task 16: Storage queries for the screens

**Files:**
- Modify: `internal/domain/types.go` (add `Installation` and `ChatSummary`)
- Create: `internal/storage/screens.go`
- Test: `internal/storage/screens_test.go`

**Interfaces:**
- Consumes: `storage.Store` (Task 10).
- Produces:
  - `domain.Installation{ID int64, GitHubInstallationID int64, AccountLogin string, AccountType string, Suspended bool}`
  - `domain.ChatSummary{ChatID int64, TelegramChatID int64, Title string, IntegrationCount int}`
  - `(*Store).InstallationsForUser(ctx context.Context, userID int64) ([]domain.Installation, error)`
  - `(*Store).ChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error)`
  - `(*Store).AddChatManager(ctx context.Context, chatID, userID int64) error`
  - `(*Store).CandidateChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error)`
  - `(*Store).CountsForUser(ctx context.Context, userID int64) (accounts, repos, chats int, err error)`
  - `(*Store).InstallationByID(ctx context.Context, id int64) (domain.Installation, error)`
  - `(*Store).ClearTopic(ctx context.Context, telegramChatID int64) error`
  - `(*Store).MarkIntegrationBroken(ctx context.Context, integrationID int64, reason string) error`

- [ ] **Step 1: Write the failing test**

Create `internal/storage/screens_test.go`:

```go
package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallationsForUserReturnsOwnedAccounts(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	otherID, _ := store.UpsertUser(ctx, 999)
	_, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)
	_, err = store.UpsertInstallation(ctx, 8, "someone-else", "User", otherID)
	require.NoError(t, err)

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "acme", found[0].AccountLogin)
	require.False(t, found[0].Suspended)
}

func TestChatsForUserCountsIntegrations(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)
	_, err = store.CreateIntegration(ctx, chatID, installID, 43, "acme/lib", userID)
	require.NoError(t, err)

	chats, err := store.ChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, chats, 1)
	require.Equal(t, "Team", chats[0].Title)
	require.Equal(t, 2, chats[0].IntegrationCount)
}

func TestCandidateChatsIncludeChatsWithNoIntegrationYet(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Fresh group", "supergroup")
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))

	// ChatsForUser is integration-based and must still be empty here.
	connected, err := store.ChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, connected)

	// The picker must still offer the chat, or the first connect is
	// impossible.
	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "Fresh group", candidates[0].Title)
}

func TestCandidateChatsExcludeOtherPeoplesChats(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	otherID, _ := store.UpsertUser(ctx, 999)
	chatID, _ := store.UpsertChat(ctx, -200, "Not yours", "supergroup")
	require.NoError(t, store.AddChatManager(ctx, chatID, otherID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, candidates)
}

func TestAddChatManagerIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")

	require.NoError(t, store.AddChatManager(ctx, chatID, userID))
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
}

func TestCountsForUserSummarisesEverything(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	accounts, repos, chats, err := store.CountsForUser(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1, accounts)
	require.Equal(t, 1, repos)
	require.Equal(t, 1, chats)
}

func TestClearTopicRemovesTopicID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	_, err := store.Pool().Exec(ctx,
		`UPDATE chats SET topic_id = 77 WHERE id = $1`, chatID)
	require.NoError(t, err)

	require.NoError(t, store.ClearTopic(ctx, -100))

	var topic *int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT topic_id FROM chats WHERE id = $1`, chatID).Scan(&topic))
	require.Nil(t, topic)
}

func TestMarkIntegrationBrokenExcludesItFromFanout(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	require.NoError(t, store.MarkIntegrationBroken(ctx, integrationID, "bot was kicked"))

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/storage/ -run 'TestInstallationsForUser|TestChatsForUser|TestCandidateChats|TestAddChatManager|TestCountsForUser|TestClearTopic|TestMarkIntegrationBroken'`
Expected: FAIL — the methods are undefined.

- [ ] **Step 3: Add the domain types**

Append to `internal/domain/types.go`:

```go
type Installation struct {
	ID                   int64
	GitHubInstallationID int64
	AccountLogin         string
	AccountType          string
	Suspended            bool
}

// ChatSummary is what the chats screen lists: one row per chat with enough
// context to pick the right one.
type ChatSummary struct {
	ChatID           int64
	TelegramChatID   int64
	Title            string
	IntegrationCount int
}
```

- [ ] **Step 4: Implement the queries**

Create `internal/storage/screens.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/domain"
)

func (s *Store) InstallationsForUser(
	ctx context.Context, userID int64,
) ([]domain.Installation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, github_installation_id, account_login, account_type,
		       suspended_at IS NOT NULL
		FROM installations WHERE user_id = $1
		ORDER BY account_login`, userID)
	if err != nil {
		return nil, fmt.Errorf("query installations: %w", err)
	}
	defer rows.Close()

	var out []domain.Installation
	for rows.Next() {
		var it domain.Installation
		if err := rows.Scan(&it.ID, &it.GitHubInstallationID,
			&it.AccountLogin, &it.AccountType, &it.Suspended); err != nil {
			return nil, fmt.Errorf("scan installation: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) InstallationByID(ctx context.Context, id int64) (domain.Installation, error) {
	var it domain.Installation
	err := s.pool.QueryRow(ctx, `
		SELECT id, github_installation_id, account_login, account_type,
		       suspended_at IS NOT NULL
		FROM installations WHERE id = $1`, id).
		Scan(&it.ID, &it.GitHubInstallationID, &it.AccountLogin,
			&it.AccountType, &it.Suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, fmt.Errorf("installation %d not found", id)
	}
	if err != nil {
		return domain.Installation{}, fmt.Errorf("load installation: %w", err)
	}
	return it, nil
}

// ChatsForUser lists chats where this user set something up, not every chat
// the bot is in — the screen must not leak other people's chats.
func (s *Store) ChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.telegram_chat_id, c.title, count(i.id)
		FROM chats c
		JOIN integrations i ON i.chat_id = c.id
		WHERE i.created_by_user_id = $1
		GROUP BY c.id, c.telegram_chat_id, c.title
		ORDER BY c.title`, userID)
	if err != nil {
		return nil, fmt.Errorf("query chats: %w", err)
	}
	defer rows.Close()

	var out []domain.ChatSummary
	for rows.Next() {
		var it domain.ChatSummary
		if err := rows.Scan(&it.ChatID, &it.TelegramChatID,
			&it.Title, &it.IntegrationCount); err != nil {
			return nil, fmt.Errorf("scan chat summary: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) AddChatManager(ctx context.Context, chatID, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_managers (chat_id, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, chatID, userID)
	if err != nil {
		return fmt.Errorf("add chat manager: %w", err)
	}
	return nil
}

// CandidateChatsForUser lists chats this user may connect a repository to:
// the ones they brought the bot into, plus any they already have an
// integration in. Telegram admin rights are re-checked at connect time, so
// this list only has to be a safe superset — never every chat the bot knows.
func (s *Store) CandidateChatsForUser(
	ctx context.Context, userID int64,
) ([]domain.ChatSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.telegram_chat_id, c.title,
		       count(i.id) FILTER (WHERE i.created_by_user_id = $1)
		FROM chats c
		LEFT JOIN integrations i ON i.chat_id = c.id
		WHERE c.id IN (
			SELECT chat_id FROM chat_managers WHERE user_id = $1
			UNION
			SELECT chat_id FROM integrations WHERE created_by_user_id = $1
		)
		GROUP BY c.id, c.telegram_chat_id, c.title
		ORDER BY c.title`, userID)
	if err != nil {
		return nil, fmt.Errorf("query candidate chats: %w", err)
	}
	defer rows.Close()

	var out []domain.ChatSummary
	for rows.Next() {
		var it domain.ChatSummary
		if err := rows.Scan(&it.ChatID, &it.TelegramChatID,
			&it.Title, &it.IntegrationCount); err != nil {
			return nil, fmt.Errorf("scan candidate chat: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CountsForUser(ctx context.Context, userID int64) (int, int, int, error) {
	var accounts, repos, chats int
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM installations WHERE user_id = $1),
			(SELECT count(*) FROM integrations WHERE created_by_user_id = $1),
			(SELECT count(DISTINCT chat_id) FROM integrations WHERE created_by_user_id = $1)
	`, userID).Scan(&accounts, &repos, &chats)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count for user: %w", err)
	}
	return accounts, repos, chats, nil
}

func (s *Store) ClearTopic(ctx context.Context, telegramChatID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chats SET topic_id = NULL WHERE telegram_chat_id = $1`, telegramChatID)
	if err != nil {
		return fmt.Errorf("clear topic: %w", err)
	}
	return nil
}

func (s *Store) MarkIntegrationBroken(ctx context.Context, integrationID int64, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE integrations SET broken_reason = $2 WHERE id = $1`, integrationID, reason)
	if err != nil {
		return fmt.Errorf("mark integration broken: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `go test ./internal/storage/...`
Expected: PASS, seventeen tests.

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/storage
git commit -m "feat: storage queries backing the dm screens"
```

---

## Task 17: DM screens

**Files:**
- Create: `internal/tg/ui/screens/home.go`
- Create: `internal/tg/ui/screens/install.go`
- Create: `internal/tg/ui/screens/accounts.go`
- Create: `internal/tg/ui/screens/repos.go`
- Create: `internal/tg/ui/screens/deps.go`
- Test: `internal/tg/ui/screens/screens_test.go`

**Interfaces:**
- Consumes: `ui.Screen`, `ui.View`, `ui.Params` (Task 15); the storage methods from Task 16; `ghapp.Client.ListRepositories` (Task 6); `render` helpers (Task 7).
- Produces: interface `screens.Store` (the subset of `*storage.Store` the screens need); interface `screens.Repos` with `ListRepositories(ctx context.Context, installationID int64) ([]ghapp.Repository, error)`; `screens.NewHome(store screens.Store) ui.Screen`; `screens.NewInstall(slug, publicURL string) ui.Screen`; `screens.NewAccounts(store screens.Store) ui.Screen`; `screens.NewRepos(store screens.Store, repos screens.Repos, pageSize int) ui.Screen`.

- [ ] **Step 1: Write the failing test**

Create `internal/tg/ui/screens/screens_test.go`:

```go
package screens_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

// fakeStore satisfies screens.Store without a database, so screen layout is
// tested in milliseconds rather than against a container.
type fakeStore struct {
	accounts, repos, chats int
	installations          []domain.Installation
	installation           domain.Installation
}

func (f *fakeStore) CountsForUser(context.Context, int64) (int, int, int, error) {
	return f.accounts, f.repos, f.chats, nil
}

func (f *fakeStore) InstallationsForUser(context.Context, int64) ([]domain.Installation, error) {
	return f.installations, nil
}

func (f *fakeStore) InstallationByID(context.Context, int64) (domain.Installation, error) {
	return f.installation, nil
}

type fakeRepos struct{ list []ghapp.Repository }

func (f fakeRepos) ListRepositories(context.Context, int64) ([]ghapp.Repository, error) {
	return f.list, nil
}

func labels(view ui.View) []string {
	var out []string
	for _, row := range view.Rows {
		for _, b := range row {
			out = append(out, b.Label)
		}
	}
	return out
}

func TestHomeWithNoInstallationOffersInstall(t *testing.T) {
	screen := screens.NewHome(&fakeStore{})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "tg-emoji")
	require.Contains(t, labels(view), "🔗 Подключить GitHub")
	require.NotContains(t, labels(view), "🏢 Репозитории")
}

func TestHomeWithInstallationShowsCounts(t *testing.T) {
	screen := screens.NewHome(&fakeStore{accounts: 2, repos: 5, chats: 3})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "2")
	require.Contains(t, view.Text, "5")
	require.Contains(t, view.Text, "3")
	require.Contains(t, labels(view), "🏢 Репозитории")
	require.Contains(t, labels(view), "💬 Чаты")
}

func TestInstallScreenLinksToGitHubWithState(t *testing.T) {
	screen := screens.NewInstall("gh-notify", "https://bot.example.com")

	view, err := screen.Render(context.Background(), ui.Session{UserID: 42, Depth: 2})
	require.NoError(t, err)

	var url string
	for _, row := range view.Rows {
		for _, b := range row {
			if b.URL != "" {
				url = b.URL
			}
		}
	}
	require.True(t, strings.HasPrefix(url, "https://github.com/apps/gh-notify/installations/new"))
	require.Contains(t, url, "state=42")
}

func TestAccountsListsEachInstallation(t *testing.T) {
	screen := screens.NewAccounts(&fakeStore{installations: []domain.Installation{
		{ID: 1, AccountLogin: "acme", AccountType: "Organization"},
		{ID: 2, AccountLogin: "octocat", AccountType: "User", Suspended: true},
	}})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, labels(view), "🏢 acme")
	// A suspended installation must be visibly different, not silently listed.
	require.Contains(t, labels(view), "⚠️ octocat")
}

func TestReposPaginates(t *testing.T) {
	list := make([]ghapp.Repository, 0, 25)
	for i := range 25 {
		list = append(list, ghapp.Repository{
			GitHubID: int64(i), FullName: "acme/repo" + string(rune('a'+i)),
		})
	}
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{list: list}, 10)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)

	require.Contains(t, labels(view), "Вперёд ▷")
	require.NotContains(t, labels(view), "◁ Назад ")

	page2, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1", "page": "1"}})
	require.NoError(t, err)
	require.Contains(t, page2.Text, "2/3")
}

func TestReposWithNoRepositoriesExplainsWhy(t *testing.T) {
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{}, 10)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Не выбрано ни одного репозитория")
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/tg/ui/screens/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Declare the dependencies the screens need**

Create `internal/tg/ui/screens/deps.go`:

```go
// Package screens holds the concrete DM screens. Each one depends on narrow
// interfaces rather than *storage.Store, so they render in tests without a
// database.
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

type Store interface {
	CountsForUser(ctx context.Context, userID int64) (accounts, repos, chats int, err error)
	InstallationsForUser(ctx context.Context, userID int64) ([]domain.Installation, error)
	InstallationByID(ctx context.Context, id int64) (domain.Installation, error)
}

type Repos interface {
	ListRepositories(ctx context.Context, installationID int64) ([]ghapp.Repository, error)
}
```

- [ ] **Step 4: Implement home and install**

Create `internal/tg/ui/screens/home.go`:

```go
package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type home struct{ store Store }

func NewHome(store Store) ui.Screen { return home{store: store} }

func (h home) Name() string { return "home" }

func (h home) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	accounts, repos, chats, err := h.store.CountsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	// A user with nothing connected gets one obvious next step instead of a
	// menu of screens that would all be empty.
	if accounts == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
				"Подключи GitHub, и события репозиториев будут приходить в твои чаты.",
			Rows: [][]ui.Button{
				{{Label: "🔗 Подключить GitHub", Screen: "install"}},
			},
		}, nil
	}

	text := fmt.Sprintf(
		"%s <b>GitHub Notify</b>\n\n%s Аккаунтов: <b>%d</b>\n%s Репозиториев: <b>%d</b>\n%s Чатов: <b>%d</b>",
		render.Emoji(render.EmojiBot, "🤖"),
		render.Emoji(render.EmojiProfile, "👤"), accounts,
		render.Emoji(render.EmojiFile, "📁"), repos,
		render.Emoji(render.EmojiPeople, "👥"), chats,
	)

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: "🏢 Репозитории", Screen: "accounts"},
				{Label: "💬 Чаты", Screen: "chats"},
			},
			{
				{Label: "📊 Статус", Screen: "status"},
				{Label: "⚙️ Настройки", Screen: "settings"},
			},
			{{Label: "➕ Добавить в чат", Screen: "add_to_chat"}},
		},
	}, nil
}
```

Create `internal/tg/ui/screens/install.go`:

```go
package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type install struct {
	slug      string
	publicURL string
}

func NewInstall(slug, publicURL string) ui.Screen {
	return install{slug: slug, publicURL: publicURL}
}

func (i install) Name() string { return "install" }

func (i install) Render(_ context.Context, s ui.Session) (ui.View, error) {
	// state carries the Telegram user id back through GitHub's redirect, so
	// the setup callback knows whose installation this is.
	url := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%d",
		i.slug, s.UserID)

	text := render.Emoji(render.EmojiLink, "🔗") + " <b>Подключение GitHub</b>\n\n" +
		"Нажми кнопку ниже, выбери аккаунт или организацию и отметь нужные репозитории. " +
		"После установки GitHub вернёт тебя обратно сюда."

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: "🔗 Установить GitHub App", URL: url}},
			{{Label: "🔄 Я установил", Screen: "accounts"}},
		},
	}, nil
}
```

- [ ] **Step 5: Implement accounts and repos**

Create `internal/tg/ui/screens/accounts.go`:

```go
package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type accounts struct{ store Store }

func NewAccounts(store Store) ui.Screen { return accounts{store: store} }

func (a accounts) Name() string { return "accounts" }

func (a accounts) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	installations, err := a.store.InstallationsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(installations) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Пока нет подключённых аккаунтов GitHub.",
			Rows: [][]ui.Button{{{Label: "🔗 Подключить GitHub", Screen: "install"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(installations)+1)
	for _, it := range installations {
		icon := "🏢"
		if it.AccountType == "User" {
			icon = "👤"
		}
		// A suspended installation cannot mint tokens, so it is labelled
		// rather than offered as if it worked.
		if it.Suspended {
			icon = "⚠️"
		}
		rows = append(rows, []ui.Button{{
			Label:  icon + " " + it.AccountLogin,
			Screen: "repos",
			Params: ui.Params{"installation": strconv.FormatInt(it.ID, 10)},
		}})
	}
	rows = append(rows, []ui.Button{{Label: "➕ Ещё аккаунт", Screen: "install"}})

	return ui.View{
		Text: render.Emoji(render.EmojiProfile, "👤") + " <b>Аккаунты GitHub</b>\n\n" +
			"Выбери, где лежит репозиторий.",
		Rows: rows,
	}, nil
}
```

Create `internal/tg/ui/screens/repos.go`:

```go
package screens

import (
	"context"
	"fmt"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type repos struct {
	store    Store
	repos    Repos
	pageSize int
}

func NewRepos(store Store, source Repos, pageSize int) ui.Screen {
	return repos{store: store, repos: source, pageSize: pageSize}
}

func (r repos) Name() string { return "repos" }

func (r repos) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	installationID, err := strconv.ParseInt(s.Params["installation"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("repos screen: bad installation param: %w", err)
	}

	installation, err := r.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}

	list, err := r.repos.ListRepositories(ctx, installation.GitHubInstallationID)
	if err != nil {
		return ui.View{}, err
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Не выбрано ни одного репозитория для этой установки.\n\n" +
				"Открой настройки GitHub App и добавь репозитории.",
			Rows: [][]ui.Button{{{Label: "🔗 Настроить доступ", Screen: "install"}}},
		}, nil
	}

	page, _ := strconv.Atoi(s.Params["page"])
	pages := (len(list) + r.pageSize - 1) / r.pageSize
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}

	start := page * r.pageSize
	end := min(start+r.pageSize, len(list))

	rows := make([][]ui.Button, 0, r.pageSize+1)
	for _, repo := range list[start:end] {
		icon := "📂"
		if repo.Private {
			icon = "🔒"
		}
		rows = append(rows, []ui.Button{{
			Label:  icon + " " + repo.FullName,
			Screen: "repo_detail",
			Params: ui.Params{
				"installation": s.Params["installation"],
				"repo":         strconv.FormatInt(repo.GitHubID, 10),
				"name":         repo.FullName,
			},
		}})
	}

	// Pagination controls appear only when there is more than one page, so a
	// short list is not cluttered with dead buttons.
	if pages > 1 {
		var nav []ui.Button
		if page > 0 {
			nav = append(nav, ui.Button{
				Label: "◁ Раньше", Screen: "repos",
				Params: ui.Params{
					"installation": s.Params["installation"],
					"page":         strconv.Itoa(page - 1),
				},
			})
		}
		if page < pages-1 {
			nav = append(nav, ui.Button{
				Label: "Вперёд ▷", Screen: "repos",
				Params: ui.Params{
					"installation": s.Params["installation"],
					"page":         strconv.Itoa(page + 1),
				},
			})
		}
		rows = append(rows, nav)
	}

	text := fmt.Sprintf("%s <b>%s</b>\n\nРепозиториев: %d",
		render.Emoji(render.EmojiFile, "📁"),
		render.Escape(installation.AccountLogin), len(list))
	if pages > 1 {
		text += fmt.Sprintf("   ·   стр. %d/%d", page+1, pages)
	}

	return ui.View{Text: text, Rows: rows}, nil
}
```

- [ ] **Step 6: Implement the placeholder screen**

`home` links to `chats`, `status`, `settings`, and `add_to_chat`, and the
`/start chat_<id>` deep link opens `chat_detail`. Those screens belong to the
follow-up plans, but a button that resolves to nothing would dead-end the
interface. One placeholder screen registered under each of those names keeps
navigation whole.

Create `internal/tg/ui/screens/placeholder.go`:

```go
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// placeholder stands in for a screen that a later plan implements. It exists
// so every button in the shipped interface leads somewhere, and so a missing
// screen is visible to the user rather than surfacing as an engine error.
type placeholder struct {
	name  string
	title string
}

func NewPlaceholder(name, title string) ui.Screen {
	return placeholder{name: name, title: title}
}

func (p placeholder) Name() string { return p.name }

func (p placeholder) Render(_ context.Context, _ ui.Session) (ui.View, error) {
	return ui.View{
		Text: render.Emoji(render.EmojiClock, "⏰") + " <b>" +
			render.Escape(p.title) + "</b>\n\nЭтот раздел ещё в работе.",
		Rows: [][]ui.Button{{{Label: "🏠 В начало", Screen: "home"}}},
	}, nil
}
```

Add this test to `internal/tg/ui/screens/screens_test.go`:

```go
func TestPlaceholderRendersTitleAndWayBack(t *testing.T) {
	screen := screens.NewPlaceholder("status", "Статус")

	require.Equal(t, "status", screen.Name())

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Статус")
	require.Contains(t, labels(view), "🏠 В начало")
}
```

- [ ] **Step 7: Run the tests and confirm they pass**

Run: `go test ./internal/tg/ui/...`
Expected: PASS, seventeen tests.

- [ ] **Step 8: Commit**

```bash
git add internal/tg/ui/screens
git commit -m "feat: home, install, accounts, repos and placeholder screens"
```

---

## Task 18: Integrate service and the connect screens

**Files:**
- Create: `internal/service/integrate.go`
- Create: `internal/tg/ui/screens/repo_detail.go`
- Create: `internal/tg/ui/screens/chat_picker.go`
- Create: `internal/tg/ui/screens/result.go`
- Test: `internal/service/integrate_test.go`
- Modify: `internal/tg/ui/screens/deps.go` (add `Chats` and `Connector` interfaces)

**Interfaces:**
- Consumes: `storage.Store` (Tasks 10 and 16), `domain.ChatSummary`.
- Produces:
  - interface `service.AdminChecker` with `IsAdmin(ctx context.Context, telegramChatID, telegramUserID int64) (bool, error)`
  - `service.NewIntegrator(store *storage.Store, admin service.AdminChecker) *service.Integrator`
  - `(*Integrator).Connect(ctx context.Context, req service.ConnectRequest) error`
  - `service.ConnectRequest{UserID, TelegramUserID, InstallationID, ChatID, TelegramChatID, RepoGitHubID int64, RepoFullName string}`
  - sentinels `service.ErrNotAdmin`, `service.ErrAlreadyConnected`
  - `screens.NewRepoDetail(store Store) ui.Screen`, `screens.NewChatPicker(chats Chats) ui.Screen`, `screens.NewResult() ui.Screen`

- [ ] **Step 1: Write the failing test**

Create `internal/service/integrate_test.go`:

```go
package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// fakeAdmin answers the admin question without calling Telegram.
type fakeAdmin struct{ allow bool }

func (f fakeAdmin) IsAdmin(context.Context, int64, int64) (bool, error) {
	return f.allow, nil
}

func newIntegrator(t *testing.T, allow bool) (*service.Integrator, *storage.Store, service.ConnectRequest) {
	t.Helper()
	ctx := context.Background()

	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)
	store, err := storage.New(ctx, testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	userID, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)

	req := service.ConnectRequest{
		UserID: userID, TelegramUserID: 555,
		InstallationID: installID,
		ChatID:         chatID, TelegramChatID: -100,
		RepoGitHubID: 42, RepoFullName: "acme/app",
	}
	return service.NewIntegrator(store, fakeAdmin{allow: allow}), store, req
}

func TestConnectCreatesIntegration(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "acme/app", found[0].RepoFullName)
}

func TestConnectRecordsAudit(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))

	var action string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT action FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&action))
	require.Equal(t, "integration.create", action)
}

func TestConnectRefusesNonAdmin(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, false)

	err := integrator.Connect(ctx, req)
	require.ErrorIs(t, err, service.ErrNotAdmin)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestConnectRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	integrator, _, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))
	require.ErrorIs(t, integrator.Connect(ctx, req), service.ErrAlreadyConnected)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/service/ -run TestConnect`
Expected: FAIL — `service.NewIntegrator` is undefined.

- [ ] **Step 3: Implement the integrator**

Create `internal/service/integrate.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/faustyu/gh-notify-go/internal/storage"
)

var (
	ErrNotAdmin         = errors.New("user is not an administrator of this chat")
	ErrAlreadyConnected = errors.New("repository is already connected to this chat")
)

// AdminChecker answers whether a user may change a chat's integrations. It is
// an interface so the check can be faked in tests and cached in production.
type AdminChecker interface {
	IsAdmin(ctx context.Context, telegramChatID, telegramUserID int64) (bool, error)
}

type ConnectRequest struct {
	UserID         int64
	TelegramUserID int64
	InstallationID int64
	ChatID         int64
	TelegramChatID int64
	RepoGitHubID   int64
	RepoFullName   string
}

type Integrator struct {
	store *storage.Store
	admin AdminChecker
}

func NewIntegrator(store *storage.Store, admin AdminChecker) *Integrator {
	return &Integrator{store: store, admin: admin}
}

// Connect wires a repository to a chat. The admin check happens here, at the
// moment of the action — checking only when the menu was drawn would let a
// demoted user act on a stale screen.
func (i *Integrator) Connect(ctx context.Context, req ConnectRequest) error {
	ok, err := i.admin.IsAdmin(ctx, req.TelegramChatID, req.TelegramUserID)
	if err != nil {
		return fmt.Errorf("check admin rights: %w", err)
	}
	if !ok {
		return ErrNotAdmin
	}

	_, err = i.store.CreateIntegration(ctx,
		req.ChatID, req.InstallationID, req.RepoGitHubID, req.RepoFullName, req.UserID)
	if err != nil {
		var pgErr *pgconn.PgError
		// 23505 is unique_violation: the (chat_id, repo_github_id) pair.
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAlreadyConnected
		}
		return err
	}

	meta, _ := json.Marshal(map[string]any{
		"repo":         req.RepoFullName,
		"installation": req.InstallationID,
	})
	if _, err := i.store.Pool().Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, chat_id, action, meta)
		VALUES ($1, $2, 'integration.create', $3)`,
		req.UserID, req.ChatID, meta); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Add the screen dependencies**

Append to `internal/tg/ui/screens/deps.go`:

```go
type Chats interface {
	// CandidateChatsForUser, not ChatsForUser: the picker must offer chats
	// that have no integration yet, otherwise the first connect is
	// impossible.
	CandidateChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error)
}
```

- [ ] **Step 5: Implement the three screens**

Create `internal/tg/ui/screens/repo_detail.go`:

```go
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type repoDetail struct{ store Store }

func NewRepoDetail(store Store) ui.Screen { return repoDetail{store: store} }

func (r repoDetail) Name() string { return "repo_detail" }

func (r repoDetail) Render(_ context.Context, s ui.Session) (ui.View, error) {
	name := s.Params["name"]

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" + render.Escape(name) + "</b>\n\n" +
		"Выбери чат, куда присылать события этого репозитория."

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{
				Label:  "💬 Подключить к чату",
				Screen: "chat_picker",
				Params: s.Params,
			}},
			{{
				Label: "🔗 Открыть на GitHub",
				URL:   "https://github.com/" + name,
			}},
		},
	}, nil
}
```

Create `internal/tg/ui/screens/chat_picker.go`:

```go
package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatPicker struct{ chats Chats }

func NewChatPicker(chats Chats) ui.Screen { return chatPicker{chats: chats} }

func (c chatPicker) Name() string { return "chat_picker" }

func (c chatPicker) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	list, err := c.chats.CandidateChatsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Бот пока не добавлен ни в один твой чат.\n\n" +
				"Добавь его в группу, и чат появится здесь.",
			Rows: [][]ui.Button{{{Label: "➕ Добавить в чат", Screen: "add_to_chat"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(list))
	for _, chat := range list {
		// Carry the repository params forward so the connect action has
		// everything it needs without a second lookup.
		params := ui.Params{
			"installation": s.Params["installation"],
			"repo":         s.Params["repo"],
			"name":         s.Params["name"],
			"chat":         strconv.FormatInt(chat.ChatID, 10),
		}
		rows = append(rows, []ui.Button{{
			Label: "💬 " + chat.Title, Screen: "connect", Params: params,
		}})
	}

	return ui.View{
		Text: render.Emoji(render.EmojiPeople, "👥") + " <b>Куда присылать</b>\n\n" +
			render.Escape(s.Params["name"]),
		Rows: rows,
	}, nil
}
```

Create `internal/tg/ui/screens/result.go`:

```go
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// result reports the outcome of a connect attempt. The callback handler puts
// "ok" or an error code in params rather than rendering ad-hoc messages, so
// every outcome lands on the same screen with the same navigation.
type result struct{}

func NewResult() ui.Screen { return result{} }

func (result) Name() string { return "result" }

func (result) Render(_ context.Context, s ui.Session) (ui.View, error) {
	var text string
	switch s.Params["status"] {
	case "ok":
		text = render.Emoji(render.EmojiCheck, "✅") + " <b>Готово</b>\n\n" +
			render.Escape(s.Params["name"]) + " подключён.\n" +
			"События уже идут — настроить их можно в разделе «Чаты»."
	case "not_admin":
		text = render.Emoji(render.EmojiCross, "❌") + " <b>Нужны права администратора</b>\n\n" +
			"Подключать репозитории к чату может только его администратор."
	case "duplicate":
		text = render.Emoji(render.EmojiInfo, "ℹ") + " <b>Уже подключено</b>\n\n" +
			render.Escape(s.Params["name"]) + " уже присылает события в этот чат."
	default:
		text = render.Emoji(render.EmojiCross, "❌") + " <b>Не получилось</b>\n\n" +
			"Попробуй ещё раз чуть позже."
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{{{Label: "🏠 В начало", Screen: "home"}}},
	}, nil
}
```

- [ ] **Step 6: Run the tests and confirm they pass**

Run: `go test ./internal/service/ ./internal/tg/...`
Expected: PASS — thirteen service tests, sixteen ui tests.

- [ ] **Step 7: Commit**

```bash
git add internal/service internal/tg/ui/screens
git commit -m "feat: integrate service with admin check and connect screens"
```

---

## Task 19: Telegram wiring

**Files:**
- Create: `internal/tg/anchor.go`
- Create: `internal/tg/handlers.go`
- Create: `internal/tg/admin.go`
- Test: `internal/tg/anchor_test.go`

**Interfaces:**
- Consumes: `ui.Engine` (Task 15), `service.Integrator` (Task 18), telego.
- Produces:
  - `tg.NewAnchor(api tg.AnchorAPI, engine *ui.Engine, nav ui.NavStore) *tg.Anchor`
  - `(*Anchor).Show(ctx context.Context, userID, telegramID int64, view ui.View) error`
  - `tg.Keyboard(ctx context.Context, engine *ui.Engine, userID int64, view ui.View) (*telego.InlineKeyboardMarkup, error)`
  - interface `tg.AnchorAPI` with `SendMessage`, `EditMessageText`
  - `tg.NewAdminChecker(api tg.AdminAPI, ttl time.Duration) *tg.AdminChecker` implementing `service.AdminChecker`
  - `tg.RegisterHandlers(bh *th.BotHandler, deps tg.HandlerDeps)`

- [ ] **Step 1: Write the failing test**

Create `internal/tg/anchor_test.go`:

```go
package tg_test

import (
	"context"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/tg"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// fakeAnchorAPI records sends and edits separately so the test can tell which
// path the anchor took.
type fakeAnchorAPI struct {
	sent    []*telego.SendMessageParams
	edited  []*telego.EditMessageTextParams
	editErr error
}

func (f *fakeAnchorAPI) SendMessage(
	_ context.Context, p *telego.SendMessageParams,
) (*telego.Message, error) {
	f.sent = append(f.sent, p)
	return &telego.Message{MessageID: 100 + len(f.sent)}, nil
}

func (f *fakeAnchorAPI) EditMessageText(
	_ context.Context, p *telego.EditMessageTextParams,
) (*telego.Message, error) {
	f.edited = append(f.edited, p)
	if f.editErr != nil {
		return nil, f.editErr
	}
	return &telego.Message{MessageID: p.MessageID}, nil
}

// memNav is an in-memory NavStore; the anchor logic needs no database.
type memNav struct {
	anchor  int
	actions map[string][2]string
}

func newMemNav() *memNav { return &memNav{actions: map[string][2]string{}} }

func (m *memNav) Push(context.Context, int64, string, ui.Params) error { return nil }
func (m *memNav) Pop(context.Context, int64) (string, ui.Params, error) {
	return "home", nil, nil
}
func (m *memNav) Depth(context.Context, int64) (int, error) { return 1, nil }
func (m *memNav) PutAction(_ context.Context, _ int64, key, screen string, _ ui.Params) error {
	m.actions[key] = [2]string{screen, ""}
	return nil
}
func (m *memNav) GetAction(_ context.Context, _ int64, key string) (string, ui.Params, error) {
	v, ok := m.actions[key]
	if !ok {
		return "", nil, ui.ErrActionNotFound
	}
	return v[0], nil, nil
}
func (m *memNav) AnchorMessageID(context.Context, int64) (int, error) { return m.anchor, nil }
func (m *memNav) SetAnchorMessageID(_ context.Context, _ int64, id int) error {
	m.anchor = id
	return nil
}

func TestShowSendsWhenNoAnchorExists(t *testing.T) {
	api := &fakeAnchorAPI{}
	nav := newMemNav()
	anchor := tg.NewAnchor(api, ui.NewEngine(nav), nav)

	err := anchor.Show(context.Background(), 1, 555, ui.View{
		Text: "hello", Rows: [][]ui.Button{{{Label: "Go", Screen: "home"}}},
	})
	require.NoError(t, err)
	require.Len(t, api.sent, 1)
	require.Empty(t, api.edited)
	require.Equal(t, 101, nav.anchor, "the new message becomes the anchor")
}

func TestShowEditsExistingAnchor(t *testing.T) {
	api := &fakeAnchorAPI{}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Empty(t, api.sent)
	require.Len(t, api.edited, 1)
	require.Equal(t, 42, api.edited[0].MessageID)
}

func TestShowFallsBackToSendWhenAnchorIsGone(t *testing.T) {
	// The user deleted the anchor message; editing it fails permanently, so
	// a fresh one must take its place instead of the screen going dead.
	api := &fakeAnchorAPI{editErr: &telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message to edit not found",
	}}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Len(t, api.edited, 1)
	require.Len(t, api.sent, 1)
	require.Equal(t, 101, nav.anchor)
}

func TestShowIgnoresUnchangedContent(t *testing.T) {
	// Telegram rejects an edit that changes nothing; tapping the current
	// screen's own button must not surface as an error.
	api := &fakeAnchorAPI{editErr: &telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message is not modified",
	}}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Empty(t, api.sent)
}

func TestKeyboardUsesOpaqueCallbackKeys(t *testing.T) {
	nav := newMemNav()
	engine := ui.NewEngine(nav)

	markup, err := tg.Keyboard(context.Background(), engine, 1, ui.View{
		Rows: [][]ui.Button{
			{{Label: "Go", Screen: "repos", Params: ui.Params{"installation": "1"}}},
			{{Label: "GitHub", URL: "https://github.com"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, markup.InlineKeyboard, 2)

	navButton := markup.InlineKeyboard[0][0]
	require.NotEmpty(t, navButton.CallbackData)
	require.LessOrEqual(t, len(navButton.CallbackData), 64)
	require.NotContains(t, navButton.CallbackData, "installation")

	linkButton := markup.InlineKeyboard[1][0]
	require.Equal(t, "https://github.com", linkButton.URL)
	require.Empty(t, linkButton.CallbackData)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/tg/ -run 'TestShow|TestKeyboard'`
Expected: FAIL — `tg.NewAnchor` is undefined.

- [ ] **Step 3: Implement the anchor**

Create `internal/tg/anchor.go`:

```go
package tg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type AnchorAPI interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
	EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error)
}

// Anchor keeps one message per user acting as the whole interface. Screens
// are edited into it instead of being appended to the chat.
type Anchor struct {
	api    AnchorAPI
	engine *ui.Engine
	nav    ui.NavStore
}

func NewAnchor(api AnchorAPI, engine *ui.Engine, nav ui.NavStore) *Anchor {
	return &Anchor{api: api, engine: engine, nav: nav}
}

func (a *Anchor) Show(ctx context.Context, userID, telegramID int64, view ui.View) error {
	markup, err := Keyboard(ctx, a.engine, userID, view)
	if err != nil {
		return err
	}

	messageID, err := a.nav.AnchorMessageID(ctx, userID)
	if err != nil {
		return err
	}

	if messageID != 0 {
		_, err := a.api.EditMessageText(ctx, &telego.EditMessageTextParams{
			ChatID:             telego.ChatID{ID: telegramID},
			MessageID:          messageID,
			Text:               view.Text,
			ParseMode:          telego.ModeHTML,
			ReplyMarkup:        markup,
			LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
		})
		if err == nil {
			return nil
		}
		// Tapping the button of the screen you are already on produces an
		// identical edit; that is a no-op, not a failure.
		if isNotModified(err) {
			return nil
		}
		if !isMessageGone(err) {
			return fmt.Errorf("edit anchor: %w", err)
		}
	}

	sent, err := a.api.SendMessage(ctx, &telego.SendMessageParams{
		ChatID:             telego.ChatID{ID: telegramID},
		Text:               view.Text,
		ParseMode:          telego.ModeHTML,
		ReplyMarkup:        markup,
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return fmt.Errorf("send anchor: %w", err)
	}
	return a.nav.SetAnchorMessageID(ctx, userID, sent.MessageID)
}

// Keyboard converts a View into Telegram markup, storing each navigation
// target behind a short opaque key.
func Keyboard(
	ctx context.Context, engine *ui.Engine, userID int64, view ui.View,
) (*telego.InlineKeyboardMarkup, error) {
	rows := make([][]telego.InlineKeyboardButton, 0, len(view.Rows))

	for _, row := range view.Rows {
		out := make([]telego.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if button.URL != "" {
				out = append(out, telego.InlineKeyboardButton{
					Text: button.Label, URL: button.URL,
				})
				continue
			}
			key, err := engine.ActionKey(ctx, userID, button.Screen, button.Params)
			if err != nil {
				return nil, err
			}
			out = append(out, telego.InlineKeyboardButton{
				Text: button.Label, CallbackData: key,
			})
		}
		if len(out) > 0 {
			rows = append(rows, out)
		}
	}
	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

func isNotModified(err error) bool {
	return descriptionContains(err, "message is not modified")
}

func isMessageGone(err error) bool {
	return descriptionContains(err, "message to edit not found") ||
		descriptionContains(err, "message can't be edited")
}

func descriptionContains(err error, needle string) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Description), needle)
}
```

- [ ] **Step 4: Implement the admin checker**

Create `internal/tg/admin.go`:

```go
package tg

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mymmrac/telego"
)

type AdminAPI interface {
	GetChatAdministrators(
		ctx context.Context, params *telego.GetChatAdministratorsParams,
	) ([]telego.ChatMember, error)
}

// AdminChecker answers "may this user manage this chat" with a short cache.
// The cache is deliberately short: a demoted admin must lose access quickly,
// but a burst of button taps should not become a burst of API calls.
type AdminChecker struct {
	api AdminAPI
	ttl time.Duration

	mu    sync.Mutex
	cache map[int64]adminEntry
}

type adminEntry struct {
	ids     map[int64]struct{}
	expires time.Time
}

func NewAdminChecker(api AdminAPI, ttl time.Duration) *AdminChecker {
	return &AdminChecker{api: api, ttl: ttl, cache: map[int64]adminEntry{}}
}

func (a *AdminChecker) IsAdmin(
	ctx context.Context, telegramChatID, telegramUserID int64,
) (bool, error) {
	a.mu.Lock()
	entry, ok := a.cache[telegramChatID]
	a.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		members, err := a.api.GetChatAdministrators(ctx,
			&telego.GetChatAdministratorsParams{ChatID: telego.ChatID{ID: telegramChatID}})
		if err != nil {
			return false, fmt.Errorf("get chat administrators: %w", err)
		}

		ids := make(map[int64]struct{}, len(members))
		for _, member := range members {
			ids[member.MemberUser().ID] = struct{}{}
		}
		entry = adminEntry{ids: ids, expires: time.Now().Add(a.ttl)}

		a.mu.Lock()
		a.cache[telegramChatID] = entry
		a.mu.Unlock()
	}

	_, isAdmin := entry.ids[telegramUserID]
	return isAdmin, nil
}
```

- [ ] **Step 5: Implement the update handlers**

Create `internal/tg/handlers.go`:

```go
package tg

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type HandlerDeps struct {
	Engine     *ui.Engine
	Anchor     *Anchor
	Store      *storage.Store
	Integrator *service.Integrator
	BotUser    string
}

// RegisterHandlers wires every Telegram update this bot reacts to. There are
// exactly three: /start, a callback tap, and being added to a group.
func RegisterHandlers(bh *th.BotHandler, deps HandlerDeps) {
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleStart(ctx, deps, message)
	}, th.CommandEqual("start"))

	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return handleCallback(ctx, deps, query)
	}, th.AnyCallbackQueryWithMessage())

	bh.HandleMyChatMember(func(ctx *th.Context, update telego.ChatMemberUpdated) error {
		return handleAddedToChat(ctx, deps, update)
	})
}

func handleStart(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	if message.From == nil {
		return nil
	}
	userID, err := deps.Store.UpsertUser(ctx, message.From.ID)
	if err != nil {
		return err
	}

	// A deep link of the form /start chat_<id> jumps straight to that chat's
	// screen, which is how the group onboarding button gets the user here.
	screen, params := "home", ui.Params(nil)
	if _, arg, found := strings.Cut(message.Text, " "); found {
		if chatArg, ok := strings.CutPrefix(strings.TrimSpace(arg), "chat_"); ok {
			screen, params = "chat_detail", ui.Params{"chat": chatArg}
		}
	}

	view, err := deps.Engine.Open(ctx, userID, message.From.ID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, message.From.ID, view)
}

func handleCallback(ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery) error {
	// Answering first stops the client's spinner even if the work below is
	// slow or fails.
	defer func() {
		_ = ctx.Bot().AnswerCallbackQuery(ctx,
			&telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	}()

	userID, err := deps.Store.UpsertUser(ctx, query.From.ID)
	if err != nil {
		return err
	}

	screen, params, err := deps.Engine.Resolve(ctx, userID, query.Data)
	if err != nil {
		if errors.Is(err, ui.ErrActionNotFound) {
			// The button came from a screen older than the action retention
			// window. Send the user home rather than failing silently.
			view, openErr := deps.Engine.Open(ctx, userID, query.From.ID, "home", nil)
			if openErr != nil {
				return openErr
			}
			return deps.Anchor.Show(ctx, userID, query.From.ID, view)
		}
		return err
	}

	if ui.IsBack(screen) {
		view, err := deps.Engine.Back(ctx, userID, query.From.ID)
		if err != nil {
			return err
		}
		return deps.Anchor.Show(ctx, userID, query.From.ID, view)
	}

	// "connect" is an action, not a screen: it performs work and then routes
	// to the result screen carrying the outcome.
	if screen == "connect" {
		return handleConnect(ctx, deps, query, userID, params)
	}

	view, err := deps.Engine.Open(ctx, userID, query.From.ID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

func handleConnect(
	ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery,
	userID int64, params ui.Params,
) error {
	installationID, _ := strconv.ParseInt(params["installation"], 10, 64)
	chatID, _ := strconv.ParseInt(params["chat"], 10, 64)
	repoID, _ := strconv.ParseInt(params["repo"], 10, 64)

	var telegramChatID int64
	if err := deps.Store.Pool().QueryRow(ctx,
		`SELECT telegram_chat_id FROM chats WHERE id = $1`, chatID).
		Scan(&telegramChatID); err != nil {
		return err
	}

	status := "ok"
	err := deps.Integrator.Connect(ctx, service.ConnectRequest{
		UserID: userID, TelegramUserID: query.From.ID,
		InstallationID: installationID,
		ChatID:         chatID, TelegramChatID: telegramChatID,
		RepoGitHubID: repoID, RepoFullName: params["name"],
	})
	switch {
	case errors.Is(err, service.ErrNotAdmin):
		status = "not_admin"
	case errors.Is(err, service.ErrAlreadyConnected):
		status = "duplicate"
	case err != nil:
		slog.Error("connect failed", "error", err)
		status = "error"
	}

	view, openErr := deps.Engine.Open(ctx, userID, query.From.ID, "result",
		ui.Params{"status": status, "name": params["name"]})
	if openErr != nil {
		return openErr
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

// handleAddedToChat greets a group exactly once, with a single button that
// carries the user into DM. The bot says nothing else in groups.
func handleAddedToChat(ctx *th.Context, deps HandlerDeps, update telego.ChatMemberUpdated) error {
	status := update.NewChatMember.MemberStatus()
	if status != telego.MemberStatusMember && status != telego.MemberStatusAdministrator {
		return nil
	}
	if update.Chat.Type == telego.ChatTypePrivate {
		return nil
	}

	chatID, err := deps.Store.UpsertChat(ctx, update.Chat.ID, update.Chat.Title, update.Chat.Type)
	if err != nil {
		return err
	}

	// Whoever added the bot becomes a candidate manager of this chat, which
	// is what puts it in their chat picker. Their admin rights are verified
	// again when they actually connect a repository.
	adderID, err := deps.Store.UpsertUser(ctx, update.From.ID)
	if err != nil {
		return err
	}
	if err := deps.Store.AddChatManager(ctx, chatID, adderID); err != nil {
		return err
	}

	_, err = ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: update.Chat.ID},
		Text:      "🤖 <b>GitHub Notify</b>\n\nНастройка — в личных сообщениях.",
		ParseMode: telego.ModeHTML,
		ReplyMarkup: &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{{{
				Text: "⚙️ Настроить",
				URL: "https://t.me/" + deps.BotUser + "?start=chat_" +
					strconv.FormatInt(chatID, 10),
			}}},
		},
	})
	return err
}
```

- [ ] **Step 6: Run the tests and confirm they pass**

Run: `go test ./internal/tg/...`
Expected: PASS — sixteen sender tests plus five anchor tests plus the ui and
screens tests.

- [ ] **Step 7: Commit**

```bash
git add internal/tg
git commit -m "feat: anchor message, admin checker and telegram update handlers"
```

---

## Task 20: Wiring and deployment

**Files:**
- Create: `cmd/bot/main.go`
- Create: `internal/httpapi/setup.go`
- Create: `deploy/Dockerfile`
- Create: `deploy/docker-compose.yml`
- Create: `deploy/Caddyfile.example`
- Create: `README.md`
- Test: `internal/httpapi/setup_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `httpapi.NewSetupHandler(store *storage.Store, tokens *ghapp.TokenSource, botUsername string) http.Handler`; `main.run(ctx context.Context) error`.

- [ ] **Step 1: Write the failing setup-callback test**

Create `internal/httpapi/setup_test.go`:

```go
package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/httpapi"
)

func TestSetupRedirectsBackToTelegram(t *testing.T) {
	handler := httpapi.NewSetupHandler(nil, nil, "gh_notify_bot")

	req := httptest.NewRequest(http.MethodGet,
		"/github/setup?installation_id=7&state=42&setup_action=install", nil)
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
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/setup?state=42", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

Note: both cases return before any storage access, so `nil` dependencies are
safe here. The installation is recorded by the `installation` webhook event,
not by this redirect — the redirect only carries the user back to Telegram.

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/httpapi/ -run TestSetup`
Expected: FAIL — `httpapi.NewSetupHandler` is undefined.

- [ ] **Step 3: Implement the setup callback**

Create `internal/httpapi/setup.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// NewSetupHandler receives GitHub's post-install redirect and bounces the
// user back into Telegram. It intentionally does not create the installation
// row: the authenticated `installation` webhook does that, and this redirect
// is not authenticated.
func NewSetupHandler(
	_ *storage.Store, _ *ghapp.TokenSource, botUsername string,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installationID := r.URL.Query().Get("installation_id")
		if installationID == "" {
			http.Error(w, "missing installation_id", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r,
			"https://t.me/"+botUsername+"?start=installed_"+installationID,
			http.StatusFound)
	})
}
```

- [ ] **Step 4: Run and confirm it passes**

Run: `go test ./internal/httpapi/`
Expected: PASS, five tests.

- [ ] **Step 5: Write main.go**

Create `cmd/bot/main.go`:

```go
// Command bot runs the Telegram bot, the GitHub webhook server, and the
// outbox workers in one process.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/config"
	_ "github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/httpapi"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/migrations"
	"github.com/faustyu/gh-notify-go/internal/tg"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to the config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *configPath); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	box, err := secret.NewBox(os.Getenv("SECRET_KEY"))
	if err != nil {
		return err
	}

	if err := migrations.Up(ctx, cfg.Database.URL); err != nil {
		return err
	}

	store, err := storage.New(ctx, cfg.Database.URL, box)
	if err != nil {
		return err
	}
	defer store.Close()

	key, err := ghapp.LoadPrivateKey(cfg.GitHub.PrivateKeyPath)
	if err != nil {
		return err
	}
	tokens := ghapp.NewTokenSource(cfg.GitHub.AppID, key,
		&http.Client{Timeout: 30 * time.Second}, store.TokenCache(), time.Now)
	github := ghapp.NewClient(tokens, &http.Client{Timeout: 30 * time.Second})

	bot, err := telego.NewBot(cfg.Bot.Token)
	if err != nil {
		return err
	}

	queue := outbox.NewQueue(store.Pool(), time.Now)
	sender := tg.NewSender(bot, func(ctx context.Context, chatID int64) error {
		return store.ClearTopic(ctx, chatID)
	})

	nav := ui.NewPostgresNav(store.Pool())
	engine := ui.NewEngine(nav)
	engine.Register(
		screens.NewHome(store),
		screens.NewInstall(cfg.GitHub.Slug, cfg.HTTP.PublicURL),
		screens.NewAccounts(store),
		screens.NewRepos(store, github, 10),
		screens.NewRepoDetail(store),
		screens.NewChatPicker(store),
		screens.NewResult(),
		// Implemented by the follow-up plans; registered now so no button
		// in the shipped interface leads nowhere.
		screens.NewPlaceholder("chats", "Чаты"),
		screens.NewPlaceholder("chat_detail", "Настройки чата"),
		screens.NewPlaceholder("status", "Статус"),
		screens.NewPlaceholder("settings", "Настройки"),
		screens.NewPlaceholder("add_to_chat", "Добавить в чат"),
	)

	integrator := service.NewIntegrator(store, tg.NewAdminChecker(bot, time.Minute))
	ingest := service.NewIngest(store, queue)

	mux := http.NewServeMux()
	mux.Handle("/gh/webhook", httpapi.NewWebhookHandler(cfg.GitHub.WebhookSecret, ingest))
	mux.Handle("/github/setup", httpapi.NewSetupHandler(store, tokens, cfg.Bot.Username))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	updates, err := bot.UpdatesViaLongPolling(ctx, nil)
	if err != nil {
		return err
	}
	handler, err := th.NewBotHandler(bot, updates)
	if err != nil {
		return err
	}
	tg.RegisterHandlers(handler, tg.HandlerDeps{
		Engine:     engine,
		Anchor:     tg.NewAnchor(bot, engine, nav),
		Store:      store,
		Integrator: integrator,
		BotUser:    cfg.Bot.Username,
	})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("http listening", "addr", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped", "error", err)
		}
	}()

	for i := range cfg.Limits.Workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			slog.Info("outbox worker started", "n", n)
			outbox.NewWorker(store.Pool(), sender, time.Now).Run(ctx, time.Second)
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := handler.Start(); err != nil {
			slog.Error("bot handler stopped", "error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = handler.StopWithContext(shutdownCtx)

	wg.Wait()
	return nil
}
```

- [ ] **Step 6: Verify the whole thing builds and every test passes**

```bash
go build ./... && go test ./...
```

Expected: build succeeds, every package passes. Fix any compile errors before
continuing — this is the first point where all the packages meet.

- [ ] **Step 7: Write the deployment files**

Create `deploy/Dockerfile`:

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/bot ./cmd/bot

FROM alpine:3.21
RUN adduser -D -u 10001 bot && apk add --no-cache ca-certificates
USER bot
COPY --from=build /out/bot /usr/local/bin/bot
ENTRYPOINT ["/usr/local/bin/bot"]
CMD ["-config", "/etc/gh-notify/config.toml"]
```

Create `deploy/docker-compose.yml`:

```yaml
services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: gh
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
      POSTGRES_DB: ghnotify
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U gh -d ghnotify"]
      interval: 5s
      timeout: 5s
      retries: 10

  bot:
    build:
      context: ..
      dockerfile: deploy/Dockerfile
    depends_on:
      db:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://gh:${POSTGRES_PASSWORD}@db:5432/ghnotify?sslmode=disable
      BOT_TOKEN: ${BOT_TOKEN:?set BOT_TOKEN}
      GITHUB_WEBHOOK_SECRET: ${GITHUB_WEBHOOK_SECRET:?set GITHUB_WEBHOOK_SECRET}
      SECRET_KEY: ${SECRET_KEY:?set SECRET_KEY}
      GITHUB_PRIVATE_KEY_PATH: /run/secrets/github_app_key
    volumes:
      - ./config.toml:/etc/gh-notify/config.toml:ro
    secrets:
      - github_app_key
    ports:
      - "127.0.0.1:8080:8080"
    restart: unless-stopped

secrets:
  github_app_key:
    file: ./secrets/github-app.pem

volumes:
  pgdata:
```

Create `deploy/Caddyfile.example`:

```
# Terminates TLS and forwards only the two public paths to the bot.
notify.example.com {
	handle /gh/webhook {
		reverse_proxy 127.0.0.1:8080
	}
	handle /github/setup {
		reverse_proxy 127.0.0.1:8080
	}
	handle {
		respond "not found" 404
	}
}
```

- [ ] **Step 8: Write the README**

Create `README.md` covering, in this order: what the bot does; the four
required environment variables (`BOT_TOKEN`, `DATABASE_URL`,
`GITHUB_WEBHOOK_SECRET`, `SECRET_KEY`, noting that `SECRET_KEY` is 32 random
bytes base64-encoded, generated with
`openssl rand -base64 32`); how to create the GitHub App (webhook URL
`https://<host>/gh/webhook`, setup URL `https://<host>/github/setup`,
repository permissions Contents/Issues/Pull requests/Metadata read, subscribed
events push/pull_request/issues); how to run `docker compose up -d` from
`deploy/`; and how to run the tests (`go test ./...`, noting that storage
tests need a working Docker daemon for testcontainers).

- [ ] **Step 9: Verify end to end against the real GitHub App**

With the stack running and the App installed on a test repository, push a
commit and confirm the message arrives. Then check the pipeline recorded it:

```bash
docker compose exec db psql -U gh -d ghnotify -c "SELECT event_kind, status, attempts FROM outbox ORDER BY id DESC LIMIT 5;"
```

Expected: a `push` row with `status = 'sent'` and `attempts = 1`. If it says
`pending` with a growing `attempts`, read `last_error` in the same table.

- [ ] **Step 10: Commit**

```bash
git add cmd deploy README.md internal/httpapi
git commit -m "feat: process wiring, setup callback and docker deployment"
```

---

## Done criteria

This plan is complete when a user can, without typing a single command beyond
`/start`: install the GitHub App from the bot's DM, pick an account, pick a
repository, pick a chat they administer, and then see `push`, `pull_request`,
and `issues` events arrive in that chat. `go test ./...` passes, and
`docker compose up -d` from `deploy/` brings the whole thing up.

The three star anti-spam layers, the remaining sixteen event types, filters,
mute, health, and the events matrix are deliberately not here — they are the
subject of the two follow-up plans.
