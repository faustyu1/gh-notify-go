# gh-notify-go — Design

Date: 2026-08-16
Status: approved, ready for implementation planning

## Purpose

A Telegram bot that delivers GitHub repository activity to group chats in real
time. This is a ground-up rewrite in Go of the existing Python project
(`github-notifi-bot`, aiogram 3 + aiogram-dialog + FastAPI + Tortoise-ORM).

The rewrite exists to fix three things the Python version cannot fix cheaply:

1. **Star spam.** A single person toggling a star on and off produces a stream
   of notifications. The current cooldown is a per-chat timer held in the
   database with no notion of *who* starred, so it either spams or silences
   everyone.
2. **Command-driven UX.** Managing integrations means remembering `/integrate`,
   `/events`, `/set_topic`, `/reinstall`. Every step spawns a new message and
   the chat history turns into a log.
3. **Dual auth complexity.** Supporting both GitHub App and personal access
   tokens forces a branch in every operation, two webhook endpoints, token
   storage, and a `/reinstall` flow to re-sync hook subscriptions.

## Decisions

These were settled during brainstorming and are not open questions:

| Area | Decision |
|---|---|
| Language / runtime | Go 1.26, single binary |
| Telegram library | `github.com/mymmrac/telego` |
| Database | PostgreSQL, fresh schema — no migration of existing data |
| DB access | `pgx` + `sqlc` (generated type-safe queries), `goose` migrations |
| GitHub auth | GitHub App only. No PAT, no OAuth |
| UI | Inline-keyboard screens in DM. No commands beyond `/start` |
| Premium emoji | Available — the bot owns a Fragment username |
| Deployment | Docker Compose (bot + Postgres) behind the operator's reverse proxy |
| Repository | New repository at `/Users/faustyu/dev/gh-notify-go` |

### Features carried over from the Python version

All 19 GitHub event types, per-chat event toggles, forum topic delivery,
admin-gated actions, structured GitHub error messages, delivery-failure DM to
the integration owner, and the pluggable one-file-per-event architecture.

### Features added in v1

- Per-integration ignore filters (author, branch, label, event action).
- Integration health (last event, 24h volume, delivery errors) and temporary
  mute.
- GitHub Markdown rendered as Telegram HTML instead of escaped raw text.
- Encrypted installation-token cache and an audit log of admin actions.

### Explicitly out of scope for v1

Mini App / WebApp interface, AI summaries of large diffs, custom message
templates, localization, org-wide subscription (`org/*`), activity statistics,
and emoji-reaction-to-GitHub-action mapping. Several of these are listed in the
Python project's TODO; they stay listed.

## Architecture

One process, layered, with a Postgres-backed outbox between ingestion and
delivery.

```
GitHub ──▶ webhook handler ──▶ outbox (Postgres) ──▶ worker pool ──▶ Telegram
                                    ▲
Telegram ──▶ update poller ──▶ UI screens ──▶ services ──▶ storage
```

### Why an outbox

The outbox is not incidental — it is what makes the new features honest rather
than best-effort:

- **Star debounce** needs to hold an event for a while and possibly cancel it.
  A row with a future `scheduled_at` does this and survives a restart; an
  in-memory timer does not.
- **Retries** need durability. A Telegram timeout must not lose the event.
- **Per-chat rate limiting** needs a queue to hold back the overflow.
- **Health data** (`last_event_at`, 24h counts, error reasons) is a read over
  rows that already exist.
- **Mute** is a worker-side skip, not a delivery-side race.

None of this requires Redis or a message broker at this traffic volume.
Postgres serves as both the database and the queue. If horizontal scaling is
ever needed, the queue is already behind an interface and can be swapped for
NATS without touching domain code.

### Package layout

```
gh-notify-go/
├── cmd/bot/main.go            # dependency wiring, graceful shutdown
├── internal/
│   ├── config/                # env + config.toml → struct, validated at boot
│   ├── domain/                # pure types; imports no infrastructure
│   ├── storage/
│   │   ├── migrations/        # goose *.sql
│   │   ├── queries/           # sqlc *.sql
│   │   └── gen/               # sqlc-generated code
│   ├── ghapp/
│   │   ├── jwt.go             # App JWT, installation tokens, TTL cache
│   │   ├── client.go          # REST: orgs, repos, commit diff
│   │   └── webhook.go         # HMAC verification, envelope parsing
│   ├── events/                # one file per event type
│   │   ├── registry.go
│   │   ├── push.go, pull_request.go, …
│   │   └── render/            # HTML helpers, markdown→HTML, emoji registry
│   ├── outbox/                # enqueue, worker pool, backoff, dedup, debounce
│   ├── tg/
│   │   ├── sender.go          # retry, message splitting, topic fallback
│   │   └── ui/                # screen engine + concrete screens
│   ├── service/               # integrate, mute, filters, health, audit
│   └── secret/                # AES-GCM for secrets stored in the database
└── deploy/                    # Dockerfile, docker-compose.yml, Caddyfile.example
```

`domain` imports no infrastructure. Services depend on repository interfaces,
so they are tested against fakes without a database.

## Data model

| Table | Purpose | Key details |
|---|---|---|
| `users` | Telegram user ↔ GitHub login | `telegram_id` unique |
| `installations` | GitHub App installation | `github_installation_id` unique, `suspended_at` |
| `chats` | chat or group, forum topic | `muted_until`, `topic_id` |
| `integrations` | repository ↔ chat | unique `(chat_id, repo_github_id)` |
| `event_settings` | per-event-type toggle | PK `(integration_id, event_kind)` |
| `filters` | ignore rules | `(integration_id, kind, pattern)` |
| `outbox` | delivery queue | `scheduled_at`, `group_key`, `attempts`, `status` |
| `star_actors` | per-actor star cooldown | `(integration_id, actor_login, last_notified_at)` |
| `gh_deliveries` | webhook deduplication | PK = `X-GitHub-Delivery`, periodically pruned |
| `ui_actions` | callback-data indirection | short key → screen action + params |
| `audit_log` | admin action trail | `actor_user_id`, `action`, `meta jsonb` |

### One deliberate change from the Python schema

Event settings and filters are keyed by **integration**, not by chat. In the
Python version, enabling `push` in a chat enables it for every repository in
that chat — which is precisely the case where one noisy repository forces you
to silence all of them. The events screen carries an "apply to every repo in
this chat" button so the old behaviour remains one tap away.

## Interface

### Screen engine

A screen is a pure function `(ctx, state) → (html, keyboard)`. The navigation
stack lives in the database, keyed by user, so `◁ Назад` always works and
survives a bot restart. Callback data is a short key into `ui_actions` rather
than serialized state: this stays under Telegram's 64-byte limit and prevents a
user from forging another user's callback.

### One anchor message

The DM holds a single "application" message that is edited in place with
`editMessageText`. Chat history stays clean — the Python version spawns a new
message per step.

### No commands

`/start` remains only as the entry point Telegram shows to new users and as the
deep-link receiver. Everything else is buttons. Text input (repository search,
filter values) uses `ForceReply`; the bot deletes the user's reply immediately
after reading it and refreshes the anchor screen.

### Screen map

```
home ─┬─ install            (no installation yet: one button to GitHub)
      ├─ accounts ── repos ── repo_detail ── chat_picker ── result
      ├─ chats ── chat_detail ─┬─ integration_detail ─┬─ events
      │                        │                     ├─ filters ── filter_add
      │                        │                     └─ health
      │                        ├─ mute
      │                        └─ topic
      ├─ status               (summary across all integrations)
      └─ settings            (anti-spam, formatting)
```

The events screen offers presets — `Всё`, `Только важное` (PR, release,
issues), `Только CI`, `Ничего` — above the 19 individual toggles, because
tapping nineteen buttons is not an interface.

### Group chats

On being added to a group the bot sends exactly one message: a heading and a
`⚙ Настроить` button carrying a deep link `t.me/<bot>?start=chat_<id>` that
opens the right screen in DM. In the group the bot listens to nothing — no
commands, no text. Administrator rights are re-checked at action time, not at
render time.

### Premium emoji

Custom emoji appear in message text only, as
`<tg-emoji emoji-id="…">🙂</tg-emoji>` under HTML parse mode, with a Unicode
fallback inside the tag for clients that cannot render it. Telegram button
labels do not support message entities in any form, so buttons use plain
Unicode emoji — this is a platform limit, not a choice.

All emoji IDs live in one registry, `events/render/emoji.go`, as named
constants (`EmojiSettings`, `EmojiRepo`, …). If Telegram rejects an entity, the
sender retries once with the tags stripped and logs it: the bot does not crash
and the message is not lost.

## Delivery pipeline

### Ingestion

`POST /gh/webhook` does the minimum and answers fast:

1. Constant-time HMAC-SHA256 verification.
2. Insert `X-GitHub-Delivery` into `gh_deliveries`; a conflict means GitHub is
   retrying, so return 200 immediately.
3. Parse the envelope; resolve integrations by
   `(repo_github_id, installation_id)`.
4. Drop events filtered out by `event_settings`, `filters`, or `muted_until`.
5. Insert rows into `outbox` holding the normalized event as `jsonb`.

Rendering happens in the worker, not here. The goal is to answer GitHub in tens
of milliseconds regardless of what Telegram is doing.

### Workers

`SELECT … FOR UPDATE SKIP LOCKED` lets a worker pool drain the outbox without
contention and without a broker. Backoff is `1s → 5s → 30s → 2m → 10m → 1h`
across six attempts; after that the row is marked `failed` and the integration
owner gets a DM.

Specific failures are handled distinctly:

- `429` — honour `retry_after`.
- `403` (bot kicked) — mark the integration broken, notify the owner, stop
  retrying.
- Forum topic deleted — retry once without `message_thread_id` and clear
  `topic_id` on the chat.

### Star anti-spam

Three layers, each covering a different scenario:

1. **Net-effect debounce.** `star.created` from actor A is queued with
   `group_key = star:<integration>:<actor>` and `scheduled_at = now + 60s`. If
   `star.deleted` arrives from the same actor within that window, the pending
   row is deleted and nothing is sent. This kills the rapid toggle.
2. **Per-actor cooldown.** `star_actors.last_notified_at` prevents the same
   actor from producing a second notification for the same repository within 24
   hours. This kills the slow cycle — unstar in the morning, star in the
   evening.
3. **Window coalescing.** Several stars from *different* actors within the
   window collapse into one message: "+N звёзд, всего 340". Viral growth stops
   spamming as a side effect.

`star.deleted` is never displayed. It exists solely as a cancellation signal.

The same windowing acts as a general safety valve: more than 20 events per chat
per minute and the remainder is coalesced into a digest.

### Secrets

In App-only mode there is little left in the database worth encrypting — there
are no PATs, and the private key is a file. The encryption work therefore goes
where it matters: the private key is supplied as a Docker secret and its
permissions are checked at boot; installation access tokens are cached in the
database (so a restart does not re-mint them) and that cache is encrypted with
AES-GCM using a key from the environment; `webhook_secret` stays in the config
file and never reaches the database. The audit log records who changed what in
which chat.

## Event rendering

One file per event type: the payload struct, a predicate deciding whether an
action is worth sending, and a render function. Registration happens in
`init()`, so adding a type remains a single new file.

Markdown in pull request and issue bodies is parsed with `goldmark` into a
custom Telegram-HTML renderer supporting headings, lists, links, inline code,
and code blocks; anything else degrades to text. Long messages are split on HTML
tag boundaries rather than byte offsets.

## Testing

Tests are written before the implementation.

- **Golden render tests** — for each of the 19 event types: a real GitHub
  payload in, expected HTML out, stored as a file. These catch formatting
  regressions that are invisible by eye.
- **Anti-spam tests** — fake repository plus an injectable clock; scenarios
  cover the rapid toggle, the slow cycle three hours later, and ten actors
  within a minute.
- **Storage tests** — `testcontainers-go` against a real Postgres, with
  migrations applied inside the test.
- **Webhook contract tests** — a bad HMAC is rejected, a repeated delivery ID
  does not duplicate, an unknown event type does not break the handler.
- **Screen engine tests** — table-driven `(screen, state) → text + button
  layout`, including an assertion that every screen except `home` offers
  `◁ Назад`.

## Open risks

- **Premium emoji entitlement.** Custom emoji require the bot to own a Fragment
  username. This is confirmed available, but the fallback path (strip tags and
  resend) is implemented anyway, because losing the entitlement should degrade
  the bot's looks and nothing else.
- **Telegram per-chat rate limits** are not published exactly. The 20/minute
  figure used by the coalescing valve is a conservative estimate and is a
  configuration value, not a constant.
- **GitHub App installation removal** arrives as a webhook. Integrations tied to
  a removed installation must be marked broken rather than silently failing on
  the next event.
