-- +goose Up
-- Gitea, Forgejo and GitVerse connect the way GitLab does: a webhook the user
-- adds by hand, with a secret the bot generated. The projects table stops
-- being GitLab's and holds whatever repositories any webhook delivered for.
ALTER TABLE gitlab_projects RENAME TO forge_projects;
ALTER TABLE forge_projects RENAME CONSTRAINT gitlab_projects_pkey TO forge_projects_pkey;
ALTER TABLE forge_projects RENAME CONSTRAINT gitlab_projects_installation_id_fkey
    TO forge_projects_installation_id_fkey;

ALTER TABLE installations DROP CONSTRAINT installations_provider_shape;
ALTER TABLE installations ADD CONSTRAINT installations_provider_shape CHECK (
    (provider = 'github' AND github_installation_id IS NOT NULL) OR
    (provider IN ('gitlab', 'gitea', 'forgejo', 'gitverse')
        AND webhook_token_hash IS NOT NULL
        AND webhook_token_ciphertext IS NOT NULL));

-- +goose Down
DELETE FROM installations WHERE provider IN ('gitea', 'forgejo', 'gitverse');
ALTER TABLE installations DROP CONSTRAINT installations_provider_shape;
ALTER TABLE installations ADD CONSTRAINT installations_provider_shape CHECK (
    (provider = 'github' AND github_installation_id IS NOT NULL) OR
    (provider = 'gitlab' AND webhook_token_hash IS NOT NULL
                         AND webhook_token_ciphertext IS NOT NULL));

ALTER TABLE forge_projects RENAME CONSTRAINT forge_projects_installation_id_fkey
    TO gitlab_projects_installation_id_fkey;
ALTER TABLE forge_projects RENAME CONSTRAINT forge_projects_pkey TO gitlab_projects_pkey;
ALTER TABLE forge_projects RENAME TO gitlab_projects;
