-- +goose Up
-- GitLab has no App to install: a connection is a webhook the user adds to a
-- project or a group, authenticated by a secret token the bot generated. The
-- token is looked up by its hash and kept sealed, so the setup screen can
-- show it again without the table holding it in the clear.
ALTER TABLE installations
    ADD COLUMN provider                 TEXT NOT NULL DEFAULT 'github',
    ALTER COLUMN github_installation_id DROP NOT NULL,
    ADD COLUMN webhook_token_hash       BYTEA UNIQUE,
    ADD COLUMN webhook_token_ciphertext BYTEA;
ALTER TABLE installations ADD CONSTRAINT installations_provider_shape CHECK (
    (provider = 'github' AND github_installation_id IS NOT NULL) OR
    (provider = 'gitlab' AND webhook_token_hash IS NOT NULL
                         AND webhook_token_ciphertext IS NOT NULL));

-- GitLab offers no way to list what a webhook covers, so the projects are
-- learned from the deliveries themselves: the first event of a project (the
-- webhook's "Test" button is enough) puts it in the picker.
CREATE TABLE gitlab_projects (
    installation_id BIGINT      NOT NULL REFERENCES installations (id) ON DELETE CASCADE,
    project_id      BIGINT      NOT NULL,
    path            TEXT        NOT NULL,
    web_url         TEXT        NOT NULL DEFAULT '',
    seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, project_id)
);

-- A GitLab project id and a GitHub repository id live in different number
-- spaces, so a repository is only unique within the installation it came
-- through. A GitHub repository belongs to exactly one installation, which
-- keeps the old rule intact there.
ALTER TABLE integrations
    DROP CONSTRAINT integrations_chat_id_repo_github_id_key,
    ADD CONSTRAINT integrations_chat_installation_repo_key
        UNIQUE (chat_id, installation_id, repo_github_id);

-- +goose Down
DELETE FROM installations WHERE provider <> 'github';
ALTER TABLE integrations
    DROP CONSTRAINT integrations_chat_installation_repo_key,
    ADD CONSTRAINT integrations_chat_id_repo_github_id_key UNIQUE (chat_id, repo_github_id);
DROP TABLE gitlab_projects;
ALTER TABLE installations
    DROP CONSTRAINT installations_provider_shape,
    DROP COLUMN webhook_token_ciphertext,
    DROP COLUMN webhook_token_hash,
    ALTER COLUMN github_installation_id SET NOT NULL,
    DROP COLUMN provider;
