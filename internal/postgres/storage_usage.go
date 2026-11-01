package postgres

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func (r *StorageSettingsRepository) RecordObject(ctx context.Context, key, task string, size int64) error {
	_, e := referenceStorageDB(ctx, r.db).ExecContext(ctx, `INSERT INTO stored_objects(owner_id,storage_key,task_id,size_bytes) VALUES($1,$2,$3,$4) ON CONFLICT(owner_id,storage_key) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,deleted_at=NULL`, identity.OwnerID(ctx), key, nullableString(task), size)
	return e
}
func (r *StorageSettingsRepository) RecordResultObject(ctx context.Context, key, task string, size int64) error {
	_, e := referenceStorageDB(ctx, r.db).ExecContext(ctx, `INSERT INTO result_objects(owner_id,storage_key,task_id,size_bytes) VALUES($1,$2,$3,$4) ON CONFLICT(owner_id,storage_key) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,deleted_at=NULL`, identity.OwnerID(ctx), key, task, size)
	return e
}
func (r *StorageSettingsRepository) RemoveObject(ctx context.Context, key string) error {
	_, e := referenceStorageDB(ctx, r.db).ExecContext(ctx, `WITH removed_results AS (UPDATE result_objects SET deleted_at=NOW() WHERE owner_id=$1 AND storage_key=$2 AND deleted_at IS NULL) UPDATE stored_objects SET deleted_at=NOW() WHERE owner_id=$1 AND storage_key=$2 AND deleted_at IS NULL`, identity.OwnerID(ctx), key)
	return e
}
func (r *StorageSettingsRepository) SharedUsage(ctx context.Context) (storage.Usage, error) {
	var u storage.Usage
	e := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0)::bigint,COUNT(*) FILTER(WHERE size_bytes IS NULL),(SELECT shared_storage_limit_bytes FROM system_settings WHERE id=TRUE) FROM stored_objects WHERE (owner_id=$1 OR ($1 IN ($2,$3) AND owner_id IN ($2,$3))) AND deleted_at IS NULL`, identity.OwnerID(ctx), identity.SharedOwnerID, templates.LibraryOwnerID).Scan(&u.UsedBytes, &u.UnknownObjects, &u.LimitBytes)
	return u, e
}
func (r *StorageSettingsRepository) PendingObjects(ctx context.Context) ([]storage.PendingObject, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT owner_id,storage_key FROM stored_objects WHERE size_bytes IS NULL AND deleted_at IS NULL ORDER BY owner_id,storage_key LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []storage.PendingObject{}
	for rows.Next() {
		var v storage.PendingObject
		if e = rows.Scan(&v.Owner, &v.Key); e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (r *StorageSettingsRepository) ResolveObject(ctx context.Context, key string, size *int64) error {
	_, e := referenceStorageDB(ctx, r.db).ExecContext(ctx, `UPDATE stored_objects SET size_bytes=$3,deleted_at=CASE WHEN $3::bigint IS NULL THEN NOW() ELSE NULL END WHERE owner_id=$1 AND storage_key=$2 AND size_bytes IS NULL AND deleted_at IS NULL`, identity.OwnerID(ctx), key, size)
	return e
}
