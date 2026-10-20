package postgres

import (
	"context"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
)

func (r *TaskRepository) StoredResultKeys(ctx context.Context, id string) ([]string, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT storage_key FROM tasks WHERE owner_id=$1 AND id=$2 AND result_deleted_at IS NULL AND storage_key IS NOT NULL UNION SELECT last_frame_storage_key FROM tasks WHERE owner_id=$1 AND id=$2 AND result_deleted_at IS NULL AND last_frame_storage_key IS NOT NULL UNION SELECT storage_key FROM stored_objects WHERE owner_id=$1 AND task_id=$2 AND deleted_at IS NULL UNION SELECT storage_key FROM result_objects WHERE owner_id=$1 AND task_id=$2 AND deleted_at IS NULL`, identity.OwnerID(ctx), id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			return nil, e
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
func (r *TaskRepository) MarkResultDeleted(ctx context.Context, id string) error {
	_, e := r.db.ExecContext(ctx, `UPDATE tasks SET result_deleted_at=COALESCE(result_deleted_at,NOW()),result_url=NULL,last_frame_url=NULL WHERE owner_id=$1 AND id=$2 AND status IN ('succeeded','failed')`, identity.OwnerID(ctx), id)
	return e
}
