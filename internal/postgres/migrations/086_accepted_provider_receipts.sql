-- +goose Up
-- Private accepted responses are durable before potentially interruptible media
-- archival. They never enter task JSON, options, reports or operation handles.
CREATE TABLE IF NOT EXISTS accepted_provider_receipts (
 task_id uuid PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 receipt bytea NOT NULL CHECK (octet_length(receipt) BETWEEN 1 AND 285212672),
 sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 progress bytea CHECK (progress IS NULL OR octet_length(progress) BETWEEN 1 AND 2097152),
 progress_sha256 text CHECK (progress_sha256 IS NULL OR progress_sha256 ~ '^[0-9a-f]{64}$'),
 CHECK ((progress IS NULL) = (progress_sha256 IS NULL))
);

-- Only a live receipt-aware worker may terminalize a paid accepted response.
-- Retain this guard on rollback: an older worker cannot query synchronous jobs
-- and must not erase their durable outputs by marking them unsupported/failed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cleanup_accepted_provider_receipt() RETURNS trigger AS $$
BEGIN
 IF NEW.status IN ('succeeded','failed') AND EXISTS (SELECT 1 FROM accepted_provider_receipts WHERE task_id=NEW.id) THEN
  IF current_setting('gateway.accepted_receipt_terminal',true) IS DISTINCT FROM NEW.id::text THEN
   RAISE EXCEPTION 'pending accepted provider receipt requires receipt-aware completion' USING ERRCODE='23514';
  END IF;
  DELETE FROM accepted_provider_receipts WHERE task_id=NEW.id;
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS tasks_cleanup_accepted_receipt ON tasks;
CREATE TRIGGER tasks_cleanup_accepted_receipt AFTER UPDATE OF status ON tasks
 FOR EACH ROW EXECUTE FUNCTION cleanup_accepted_provider_receipt();

-- +goose Down
-- Preserve accepted paid responses and archive checkpoints during the supported
-- application rollback window. Old code may read and update ordinary fields,
-- but its unsupported terminal writes are rejected by the retained guard. A
-- receipt-aware worker must finish these pending jobs after re-upgrade; old
-- code cannot process or discard them. The receipt is never resubmitted.
SELECT 1;
