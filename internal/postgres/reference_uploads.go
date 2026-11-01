package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func (r *ReferenceRepository) CreateUpload(ctx context.Context, s references.UploadSession) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return errors.New("upload admission requires a database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	policy, err := lockAdmissionPolicy(ctx, tx)
	if err != nil {
		return err
	}
	if err = checkAdmissionPolicy(ctx, tx, policy); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO reference_uploads(owner_id,id,state,expires_at,actor_id) VALUES ($1,$2,$3,$4,$5)`, identity.OwnerID(ctx), s.Item.ID, data, s.ExpiresAt, nullableString(identity.ActorID(ctx)))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ReferenceRepository) UseUpload(ctx context.Context, id string, remove bool, fn func(context.Context, *references.UploadSession, references.Repository) error) error {
	if _, err := uuid.Parse(id); err != nil {
		return storage.ErrObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, ok := r.db.(*sql.DB)
	if !ok {
		return errors.New("upload requires a database transaction")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT state FROM reference_uploads WHERE owner_id=$1 AND id=$2 AND ($3='' OR actor_id::text=$3) FOR UPDATE`, identity.OwnerID(ctx), id, identity.ContentAuthorFilter(ctx)).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrObjectNotFound
	}
	if err != nil {
		return err
	}
	var session references.UploadSession
	if err = json.Unmarshal(data, &session); err != nil {
		return err
	}
	// Identical files may finish concurrently on different gateway instances.
	// Lock only this owner's digest, never all uploads or all owners.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity.OwnerID(ctx)+":"+session.Item.SHA256); err != nil {
		return err
	}
	if err = fn(context.WithValue(ctx, referenceTransactionKey{}, tx), &session, &ReferenceRepository{db: tx}); err != nil {
		return err
	}
	if remove {
		_, err = tx.ExecContext(ctx, `DELETE FROM reference_uploads WHERE owner_id=$1 AND id=$2`, identity.OwnerID(ctx), id)
	} else {
		data, err = json.Marshal(session)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE reference_uploads SET state=$3 WHERE owner_id=$1 AND id=$2`, identity.OwnerID(ctx), id, data)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ReferenceRepository) ExpiredUploads(ctx context.Context) ([]references.ExpiredUpload, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT owner_id,id FROM reference_uploads WHERE expires_at < now()-interval '1 hour' ORDER BY expires_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []references.ExpiredUpload
	for rows.Next() {
		var item references.ExpiredUpload
		if err = rows.Scan(&item.Owner, &item.ID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Storage metadata and object accounting participate in the upload transaction.
// Reuse its connection instead of acquiring a nested pool connection, which can
// deadlock when concurrent uploads occupy the entire pool.
type referenceTransactionKey struct{}

func referenceStorageDB(ctx context.Context, db *sql.DB) referenceDB {
	if tx, ok := ctx.Value(referenceTransactionKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}
