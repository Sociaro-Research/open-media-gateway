-- +goose Up
ALTER TABLE email_auth_challenges DROP CONSTRAINT email_auth_challenges_purpose_check;
ALTER TABLE email_auth_challenges ADD CONSTRAINT email_auth_challenges_purpose_check
    CHECK (purpose IN ('verify', 'setup', 'reset'));
-- Legacy pending passwords must never be activated by confirmation links.
-- Keep the column for compatibility; new registrations always leave it empty.
UPDATE email_auth_challenges SET password_hash='' WHERE purpose='verify';

-- +goose Down
DELETE FROM email_auth_challenges WHERE purpose='setup';
ALTER TABLE email_auth_challenges DROP CONSTRAINT email_auth_challenges_purpose_check;
ALTER TABLE email_auth_challenges ADD CONSTRAINT email_auth_challenges_purpose_check
    CHECK (purpose IN ('verify', 'reset'));
