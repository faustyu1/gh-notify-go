-- +goose Up
-- The `installation created` webhook arrives before anyone has claimed the
-- installation through the setup redirect, so the row must exist without an
-- owner; InstallationsForUser only lists owned ones.
ALTER TABLE installations ALTER COLUMN user_id DROP NOT NULL;

-- +goose Down
ALTER TABLE installations ALTER COLUMN user_id SET NOT NULL;
