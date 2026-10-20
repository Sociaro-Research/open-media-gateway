-- +goose Up
-- Private result attempts need a deletion index, separate from shared quota.
-- Historical stored_objects rows retain their existing quota semantics.
CREATE TABLE result_objects (
 owner_id UUID NOT NULL,
 storage_key TEXT NOT NULL,
 task_id UUID NOT NULL,
 size_bytes BIGINT NOT NULL CHECK(size_bytes >= 0),
 deleted_at TIMESTAMPTZ,
 PRIMARY KEY(owner_id,storage_key)
);
CREATE INDEX result_objects_live_task ON result_objects(owner_id,task_id) WHERE deleted_at IS NULL;

-- Application rollback leaves this additive table and its records in place.
-- +goose Down
DROP TABLE result_objects;
