-- +goose Up
-- Referral links for ad campaigns: one code per placement, counted on every
-- /start that carries it. A user is attributed to the first link that
-- brought them in and never re-attributed.
CREATE TABLE ref_links (
    id                 BIGSERIAL PRIMARY KEY,
    code               TEXT        NOT NULL UNIQUE,
    name               TEXT        NOT NULL,
    starts             BIGINT      NOT NULL DEFAULT 0,
    created_by_user_id BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
    ADD COLUMN ref_link_id  BIGINT REFERENCES ref_links (id) ON DELETE SET NULL,
    -- Refreshed on every interaction; the admin statistics count active
    -- users from it without recording what they did.
    ADD COLUMN last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set when a broadcast learns the user blocked the bot, cleared by the
    -- next interaction.
    ADD COLUMN blocked_at   TIMESTAMPTZ;
CREATE INDEX users_ref_link_idx ON users (ref_link_id) WHERE ref_link_id IS NOT NULL;

-- A broadcast copies one message the admin sent to the bot into every
-- user's DM. last_user_id is the last users.id handled, so a restart resumes where
-- the previous process stopped instead of messaging anyone twice.
CREATE TABLE broadcasts (
    id                 BIGSERIAL PRIMARY KEY,
    created_by_user_id BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    source_chat_id     BIGINT      NOT NULL,
    source_message_id  BIGINT      NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'draft',
    total              INT         NOT NULL DEFAULT 0,
    sent               INT         NOT NULL DEFAULT 0,
    failed             INT         NOT NULL DEFAULT 0,
    last_user_id       BIGINT      NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at         TIMESTAMPTZ,
    finished_at        TIMESTAMPTZ
);
CREATE INDEX broadcasts_running_idx ON broadcasts (id) WHERE status = 'running';

-- +goose Down
DROP TABLE broadcasts;
ALTER TABLE users DROP COLUMN ref_link_id, DROP COLUMN last_seen_at, DROP COLUMN blocked_at;
DROP TABLE ref_links;
