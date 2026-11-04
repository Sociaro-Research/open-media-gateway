package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"time"
)

func referenceDeletionEqual(a, b references.Image) bool {
	return a.ID == b.ID && a.StorageKey == b.StorageKey && a.StorageOwnerID == b.StorageOwnerID && a.SHA256 == b.SHA256 && a.SizeBytes == b.SizeBytes
}
func admitReferenceDeletion(ctx context.Context, tx *sql.Tx, id string, expected *references.Image) (references.Deletion, error) {
	d := references.Deletion{Owner: identity.OwnerID(ctx), Lease: uuid.NewString()}
	e := tx.QueryRowContext(ctx, `SELECT id::text,storage_key,COALESCE(storage_owner_id::text,owner_id::text),COALESCE(content_sha256,''),size_bytes FROM reference_images WHERE owner_id=$1 AND id=$2 AND ($3='' OR actor_id::text=$3) FOR UPDATE`, d.Owner, id, identity.ContentAuthorFilter(ctx)).Scan(&d.Item.ID, &d.Item.StorageKey, &d.Item.StorageOwnerID, &d.Item.SHA256, &d.Item.SizeBytes)
	if errors.Is(e, sql.ErrNoRows) {
		return d, storage.ErrObjectNotFound
	}
	if e != nil {
		return d, e
	}
	if expected != nil && !referenceDeletionEqual(*expected, d.Item) {
		return d, references.ErrInUse
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO reference_deletion_admissions(owner_id,reference_id,storage_owner_id,storage_key,content_sha256,size_bytes) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes)
	if e != nil {
		return d, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE reference_deletion_admissions SET lease_token=$7,lease_until=clock_timestamp()+INTERVAL '1 minute',last_attempt=clock_timestamp() WHERE owner_id=$1 AND reference_id=$2 AND storage_owner_id=$3 AND storage_key=$4 AND content_sha256=$5 AND size_bytes=$6 AND state='pending' AND lease_until<=clock_timestamp()`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes, d.Lease)
	if e != nil {
		return d, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return d, references.ErrInUse
	}
	_, e = tx.ExecContext(ctx, `UPDATE reference_images SET deleting=true WHERE owner_id=$1 AND id=$2`, d.Owner, d.Item.ID)
	return d, e
}
func (r *ReferenceRepository) ClaimDeletions(ctx context.Context, limit int) ([]references.Deletion, error) {
	if limit < 1 || limit > 100 {
		return nil, references.ErrInUse
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return nil, references.ErrInUse
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT owner_id::text,reference_id::text,storage_owner_id::text,storage_key,content_sha256,size_bytes FROM reference_deletion_admissions WHERE state='pending' AND lease_until<=clock_timestamp() ORDER BY last_attempt,created_at,owner_id,reference_id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if e != nil {
		return nil, e
	}
	items := []references.Deletion{}
	for rows.Next() {
		var d references.Deletion
		if e = rows.Scan(&d.Owner, &d.Item.ID, &d.Item.StorageOwnerID, &d.Item.StorageKey, &d.Item.SHA256, &d.Item.SizeBytes); e != nil {
			rows.Close()
			return nil, e
		}
		d.Lease = uuid.NewString()
		items = append(items, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, d := range items {
		var mismatched bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE owner_id=$1 AND id=$2 AND (NOT deleting OR COALESCE(storage_owner_id,owner_id)<>$3::uuid OR storage_key<>$4 OR COALESCE(content_sha256,'')<>$5 OR size_bytes<>$6))`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes).Scan(&mismatched)
		if e != nil {
			return nil, e
		}
		if mismatched {
			return nil, references.ErrInUse
		}
		if _, e = tx.ExecContext(ctx, `UPDATE reference_deletion_admissions SET lease_token=$3,lease_until=clock_timestamp()+INTERVAL '2 minutes',last_attempt=clock_timestamp() WHERE owner_id=$1 AND reference_id=$2`, d.Owner, d.Item.ID, d.Lease); e != nil {
			return nil, e
		}
	}
	return items, tx.Commit()
}
func (r *ReferenceRepository) FinishDeletion(ctx context.Context, d references.Deletion, success bool) error {
	db, ok := r.db.(*sql.DB)
	if !ok {
		return references.ErrInUse
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = sourceAssetLock(ctx, tx, d.Owner, d.Item.ID); e != nil {
		return e
	}
	var state string
	e = tx.QueryRowContext(ctx, `SELECT state FROM reference_deletion_admissions WHERE owner_id=$1 AND reference_id=$2 AND storage_owner_id=$3 AND storage_key=$4 AND content_sha256=$5 AND size_bytes=$6 AND lease_token=$7 FOR UPDATE`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes, d.Lease).Scan(&state)
	if e != nil {
		return e
	}
	if state == "completed" {
		return tx.Commit()
	}
	if !success {
		_, e = tx.ExecContext(ctx, `UPDATE reference_deletion_admissions SET lease_token=NULL,lease_until=clock_timestamp(),last_attempt=clock_timestamp() WHERE owner_id=$1 AND reference_id=$2`, d.Owner, d.Item.ID)
		if e != nil {
			return e
		}
		return tx.Commit()
	}
	var mismatched bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE owner_id=$1 AND id=$2 AND (NOT deleting OR COALESCE(storage_owner_id,owner_id)<>$3::uuid OR storage_key<>$4 OR COALESCE(content_sha256,'')<>$5 OR size_bytes<>$6))`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes).Scan(&mismatched)
	if e != nil {
		return e
	}
	if mismatched {
		return references.ErrInUse
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM reference_images WHERE owner_id=$1 AND id=$2 AND deleting AND COALESCE(storage_owner_id,owner_id)=$3::uuid AND storage_key=$4 AND COALESCE(content_sha256,'')=$5 AND size_bytes=$6`, d.Owner, d.Item.ID, d.Item.StorageOwnerID, d.Item.StorageKey, d.Item.SHA256, d.Item.SizeBytes); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE reference_deletion_admissions SET state='completed',completed_at=$3 WHERE owner_id=$1 AND reference_id=$2`, d.Owner, d.Item.ID, time.Now()); e != nil {
		return e
	}
	return tx.Commit()
}
