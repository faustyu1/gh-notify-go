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
-- to offer before the first integration exists, and listing every known chats
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
