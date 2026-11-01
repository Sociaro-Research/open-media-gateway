-- +goose Up
ALTER TABLE user_api_keys ADD COLUMN purpose text NOT NULL DEFAULT '' CHECK (purpose IN ('','playground_templates'));
CREATE TABLE playground_key_slots (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 workspace text NOT NULL,
 key_id uuid NOT NULL UNIQUE REFERENCES user_api_keys(id) ON DELETE CASCADE,
 ciphertext bytea NOT NULL,
 PRIMARY KEY(user_id,workspace)
);
-- Retain request-to-key identity after rotation, without retaining old secrets.
CREATE TABLE playground_key_requests (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 workspace text NOT NULL,
 key_id uuid NOT NULL REFERENCES user_api_keys(id) ON DELETE CASCADE,
 PRIMARY KEY(user_id,request_id)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_api_key_purpose() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.purpose IS DISTINCT FROM OLD.purpose THEN
  RAISE EXCEPTION 'API key purpose is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER api_key_purpose_immutable BEFORE UPDATE ON user_api_keys FOR EACH ROW EXECUTE FUNCTION preserve_api_key_purpose();

-- +goose Down
-- Keep encrypted account associations and immutable key provenance on rollback.
SELECT 1;
