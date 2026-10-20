-- +goose Up
ALTER TABLE system_settings ADD COLUMN shared_storage_limit_bytes BIGINT CHECK(shared_storage_limit_bytes >= 0);
ALTER TABLE tasks ADD COLUMN result_deleted_at TIMESTAMPTZ;
CREATE TABLE stored_objects (
 owner_id UUID NOT NULL,
 storage_key TEXT NOT NULL,
 task_id UUID,
 size_bytes BIGINT CHECK(size_bytes >= 0),
 deleted_at TIMESTAMPTZ,
 PRIMARY KEY(owner_id,storage_key)
);
CREATE INDEX stored_objects_live_user ON stored_objects(owner_id) WHERE deleted_at IS NULL;
CREATE INDEX stored_objects_task ON stored_objects(owner_id,task_id);
-- Existing shared S3 references retain their original connection identity.
INSERT INTO stored_objects(owner_id,storage_key,size_bytes)
 SELECT r.owner_id,r.storage_key,r.size_bytes FROM reference_images r
 JOIN storage_connections c ON r.storage_key LIKE 's3:'||c.id::text||'/%'
 WHERE c.owner_id='00000000-0000-0000-0000-000000000001'
 ON CONFLICT DO NOTHING;
INSERT INTO stored_objects(owner_id,storage_key,task_id,size_bytes)
 SELECT owner_id,storage_key,id,result_size_bytes FROM tasks
 WHERE storage_owner_id='00000000-0000-0000-0000-000000000001' AND storage_key IS NOT NULL
 ON CONFLICT DO NOTHING;
-- Legacy last-frame sizes are reconciled from storage metadata by the worker.
INSERT INTO stored_objects(owner_id,storage_key,task_id)
 SELECT owner_id,last_frame_storage_key,id FROM tasks
 WHERE storage_owner_id='00000000-0000-0000-0000-000000000001' AND last_frame_storage_key IS NOT NULL
 ON CONFLICT DO NOTHING;
-- +goose Down
DROP TABLE stored_objects;
ALTER TABLE tasks DROP COLUMN result_deleted_at;
ALTER TABLE system_settings DROP COLUMN shared_storage_limit_bytes;
