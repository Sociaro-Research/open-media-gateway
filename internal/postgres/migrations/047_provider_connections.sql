-- +goose Up
ALTER TABLE provider_settings ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE provider_settings ADD COLUMN archived boolean NOT NULL DEFAULT false;
ALTER TABLE provider_settings ADD COLUMN version bigint NOT NULL DEFAULT 1;

CREATE TABLE provider_setting_revisions (
    owner_id uuid NOT NULL,
    provider_id text NOT NULL,
    fingerprint text NOT NULL,
    credentials_ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, provider_id, fingerprint),
    FOREIGN KEY (owner_id, provider_id) REFERENCES provider_settings(owner_id, provider_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back provider connections'; END $$;
-- +goose StatementEnd
