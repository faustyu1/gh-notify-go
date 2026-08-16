-- +goose Up
-- The install link used to carry users.id as GitHub's `state`, which made the
-- setup redirect forgeable: anyone could hand it an installation id and a user
-- id and take ownership. A state is now an unguessable single-use token that
-- expires, and it is the only thing the redirect is trusted to carry.
CREATE TABLE install_states (
    token      TEXT        PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX install_states_expires_idx ON install_states (expires_at);

-- +goose Down
DROP TABLE install_states;
