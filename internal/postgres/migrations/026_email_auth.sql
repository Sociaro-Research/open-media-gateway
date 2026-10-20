-- +goose Up
-- Verified identities share a canonical address. Never merge existing owners,
-- memberships or credentials implicitly if a deployment contains duplicates.
CREATE UNIQUE INDEX users_email_unique_idx ON users (LOWER(email));
ALTER TABLE users ALTER COLUMN google_subject DROP NOT NULL;

CREATE TABLE email_credentials (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Unverified registration is separate from users and carries no permissions.
CREATE TABLE email_auth_challenges (
    token_hash BYTEA PRIMARY KEY,
    email TEXT NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    password_hash TEXT NOT NULL DEFAULT '',
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (email, purpose)
);
CREATE INDEX email_auth_challenges_expiry_idx ON email_auth_challenges(expires_at);

CREATE TABLE email_auth_limits (
    key_hash BYTEA PRIMARY KEY,
    attempts INTEGER NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX email_auth_limits_expiry_idx ON email_auth_limits(expires_at);

-- +goose Down
DROP TABLE email_auth_limits;
DROP TABLE email_auth_challenges;
DROP TABLE email_credentials;
-- Email-only users must not be deleted when rolling back application code.
DROP INDEX users_email_unique_idx;
