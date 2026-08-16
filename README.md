# gh-notify-go

A Telegram bot that delivers GitHub repository events to group chats in real
time. Everything is driven by inline keyboards in a private chat with the bot;
`/start` is the only command.

A live instance runs at [@g0thubbot](https://t.me/g0thubbot) — send it `/start`
to see the interface before deploying your own.

Architecture: a single Go binary. The GitHub webhook verifies the HMAC,
deduplicates by delivery id, finds the matching integrations, and writes rows
into a Postgres outbox. A pool of workers drains the outbox, renders each event
as Telegram HTML, and sends it with retries and backoff.

## Events

19 kinds: `push`, `pull_request`, `pull_request_review`,
`pull_request_review_comment`, `issues`, `issue_comment`, `commit_comment`,
`release`, `star`, `fork`, `create`, `delete`, `gollum`, `member`, `public`,
`deployment`, `deployment_status`, `check_suite`, `workflow_run`.

Every kind is toggled per integration, plus three presets: everything, the
important ones, nothing. Filters match on author, branch, label, and action.
`star` fires once per user per repository, forever. When a chat hits the
`CHAT_PER_MINUTE` ceiling, the excess collapses into a digest.

## Chat permissions

Connecting a repository to a chat and changing its settings is limited to
administrators of that chat. Rights are asked of Telegram at the moment of the
action (cached for a minute), not at the moment the screen is drawn. Viewing is
covered by the same check: the chat screen and the event, filter, and health
screens are all behind it.

## Environment variables

| Variable | Purpose |
|---|---|
| `BOT_TOKEN` | bot token from @BotFather |
| `BOT_USERNAME` | bot username without `@` |
| `DATABASE_URL` | `postgres://…` connection string (SQLite is not supported) |
| `GITHUB_APP_ID`, `GITHUB_SLUG` | GitHub App id and slug |
| `GITHUB_PRIVATE_KEY_PATH` | path to the App's `.pem` private key |
| `GITHUB_WEBHOOK_SECRET` | GitHub App webhook secret |
| `PUBLIC_URL` | external address the reverse proxy points at |
| `SECRET_KEY` | AES-GCM key encrypting installation tokens — 32 random bytes in base64, generate with `openssl rand -base64 32` |
| `CHAT_PER_MINUTE`, `WORKERS` | delivery pace and worker count |

Configuration is environment variables only. The full list with defaults lives
in `.env.example`: copy it to `.env` and the binary reads it at startup. Real
environment variables always win over the file.

## Creating the GitHub App

1. Open https://github.com/settings/apps/new.
2. Webhook URL: `https://<host>/gh/webhook`, secret: the value of
   `GITHUB_WEBHOOK_SECRET`.
3. Setup URL: `https://<host>/github/setup`, with "Redirect on update" checked.
4. Permissions → Repository: **Metadata: read, Contents: read, Issues: read,
   Pull requests: read, Deployments: read, Checks: read, Actions: read**.
5. Subscribe to the events you want from the list above; at minimum `push`,
   `pull_request`, `issues`, `installation`.
6. Download the `.pem` private key and put it in `deploy/secrets/github-app.pem`
   with mode `0600`.

The install link is handed out by the bot and carries a single-use `state`
token valid for an hour; that token, and nothing else, decides whose
installation it is. An installation is bound to a user once — changing the
owner means removing and re-installing the App.

A public App also needs a homepage URL and a privacy policy URL in its GitHub
settings. With @BotFather, set the description, the about text, and a picture;
there is one command, `/start`.

## Running

```bash
cd deploy
cp .env.example .env   # fill in DATABASE_URL, BOT_TOKEN, …
docker compose up -d
```

The database is external by default: `DATABASE_URL` points at your own cluster,
with `sslmode=require` if it is reachable outside a private network. The schema
migrates itself at startup, so that user needs DDL rights on the database. The
bundled Postgres is an option for a self-contained deployment:

```bash
docker compose -f docker-compose.yml -f docker-compose.local-db.yml up -d
```

The bot listens on `127.0.0.1:8080` and is published by a reverse proxy — see
`deploy/Caddyfile.example`. `GET /healthz` pings Postgres and answers 503 when
the database is unreachable; the container healthcheck uses the same endpoint.

### Cloudflare Tunnel

An alternative to a proxy on a public IP: `cloudflared` holds an outbound
connection to Cloudflare, the server has no inbound ports, and its address is
visible to neither GitHub nor a scanner. Requires a domain on Cloudflare.

```bash
cloudflared tunnel login
cloudflared tunnel create ghnotify
cloudflared tunnel route dns ghnotify notify.example.com
cp ~/.cloudflared/<TUNNEL-ID>.json deploy/cloudflared/
cp deploy/cloudflared/config.example.yml deploy/cloudflared/config.yml
```

Put your tunnel id and host into `config.yml`; only `/gh/webhook` and
`/github/setup` are exposed, everything else is a 404. Then:

```bash
docker compose -f docker-compose.yml -f docker-compose.cloudflared.yml up -d
```

`PUBLIC_URL` is the same `https://notify.example.com` used in the tunnel route.
Telegram updates arrive over long polling anyway, so the only inbound traffic
is the GitHub webhook, and the tunnel covers that path entirely.

**Run exactly one instance.** Telegram updates are fetched by long polling, and
two copies would fight over `getUpdates`. Migrations take an advisory lock at
startup, so a simultaneous restart will not corrupt the database, but the
process cannot be scaled horizontally.

## Data retention

A janitor sweeps hourly so the tables that only grow stay bounded: delivered
outbox rows after an hour (the per-chat rate valve looks back sixty seconds),
failed ones after 7 days, callback keys after 7 days, GitHub delivery ids after
7 days, and audit records after 90 days. Pending outbox rows are never swept at
any age. An expired callback key is not an error: the button lands the user on
the home screen instead.

## Languages

Every user-visible string lives in `internal/i18n/locales/*.yaml` and is
embedded in the binary. Five locales: English (the base and the fallback),
Russian, Spanish, German, Portuguese.

The private interface's language comes from Telegram's `language_code` on the
first `/start` and can be changed from the Settings screen. A notification's
language is the language of the integration's owner, so two integrations in one
chat can speak differently. A test in `internal/i18n` enforces that every locale
carries the same set of keys and placeholders.

## Tests

```bash
make test        # one throwaway Postgres container for the whole run
make test-direct # no Makefile: testcontainers boots Postgres itself
```

Each test gets its own database inside the shared container, so `make test`
starts exactly one Postgres rather than one per test.
