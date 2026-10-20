-- +goose Up
-- Only encrypted invitation capabilities survive email verification and grant exchange.
ALTER TABLE email_auth_challenges ADD COLUMN invitation_ciphertext BYTEA;

-- +goose Down
ALTER TABLE email_auth_challenges DROP COLUMN invitation_ciphertext;
