package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
)

type referenceDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type ReferenceRepository struct{ db referenceDB }

func NewReferenceRepository(db *sql.DB) *ReferenceRepository { return &ReferenceRepository{db: db} }
func (r *ReferenceRepository) List(ctx context.Context, limit, offset int) ([]references.Image, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, content_type, size_bytes, storage_key, created_at, COALESCE(content_sha256, '') FROM reference_images WHERE owner_id=$1 AND ($4='' OR actor_id::text=$4) ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, identity.OwnerID(ctx), limit, offset, identity.ContentAuthorFilter(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []references.Image{}
	for rows.Next() {
		var item references.Image
		if err := rows.Scan(&item.ID, &item.Name, &item.ContentType, &item.SizeBytes, &item.StorageKey, &item.CreatedAt, &item.SHA256); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *ReferenceRepository) Get(ctx context.Context, id string) (references.Image, error) {
	var item references.Image
	err := r.db.QueryRowContext(ctx, `SELECT id, name, content_type, size_bytes, storage_key, created_at, COALESCE(content_sha256, '') FROM reference_images WHERE owner_id=$1 AND id=$2 AND ($3='' OR actor_id::text=$3)`, identity.OwnerID(ctx), id, identity.ContentAuthorFilter(ctx)).Scan(&item.ID, &item.Name, &item.ContentType, &item.SizeBytes, &item.StorageKey, &item.CreatedAt, &item.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		err = storage.ErrObjectNotFound
	}
	return item, err
}
func (r *ReferenceRepository) Insert(ctx context.Context, item references.Image) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO reference_images (owner_id,id,name,content_type,size_bytes,storage_key,created_at,content_sha256,actor_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, identity.OwnerID(ctx), item.ID, item.Name, item.ContentType, item.SizeBytes, item.StorageKey, item.CreatedAt, nullableString(item.SHA256), nullableString(identity.ActorID(ctx)))
	return referenceWriteError(err)
}
func (r *ReferenceRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM reference_images WHERE owner_id=$1 AND id=$2 AND ($3='' OR actor_id::text=$3)`, identity.OwnerID(ctx), id, identity.ContentAuthorFilter(ctx))
	return err
}

func referenceWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "reference_images_owner_hash" {
		return references.ErrDuplicate
	}
	return err
}
func (r *ReferenceRepository) FindByHash(ctx context.Context, digest string) (references.Image, error) {
	var item references.Image
	err := r.db.QueryRowContext(ctx, `SELECT id,name,content_type,size_bytes,storage_key,created_at,content_sha256 FROM reference_images WHERE owner_id=$1 AND content_sha256=$2 AND ($3='' OR actor_id::text=$3)`, identity.OwnerID(ctx), digest, identity.ContentAuthorFilter(ctx)).Scan(&item.ID, &item.Name, &item.ContentType, &item.SizeBytes, &item.StorageKey, &item.CreatedAt, &item.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		err = storage.ErrObjectNotFound
	}
	return item, err
}
func (r *ReferenceRepository) UnhashedBySize(ctx context.Context, size int64) ([]references.Image, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,content_type,size_bytes,storage_key,created_at FROM reference_images WHERE owner_id=$1 AND size_bytes=$2 AND content_sha256 IS NULL AND ($3='' OR actor_id::text=$3) ORDER BY created_at,id`, identity.OwnerID(ctx), size, identity.ContentAuthorFilter(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []references.Image
	for rows.Next() {
		var item references.Image
		if err := rows.Scan(&item.ID, &item.Name, &item.ContentType, &item.SizeBytes, &item.StorageKey, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *ReferenceRepository) SetHash(ctx context.Context, id, digest string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE reference_images SET content_sha256=$3 WHERE owner_id=$1 AND id=$2 AND content_sha256 IS NULL`, identity.OwnerID(ctx), id, digest)
	return referenceWriteError(err)
}

// Keep the source available until asynchronous provider ingestion is resolved.
func (r *ReferenceRepository) DeleteWithGuard(ctx context.Context, id string, remove func() error) error {
	db, ok := r.db.(*sql.DB)
	if !ok {
		return references.ErrInUse
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = sourceAssetLock(ctx, tx, identity.OwnerID(ctx), id); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_assets WHERE owner_id=$1 AND source_id=$2 AND status IN ('Creating','Unknown','Processing','Disconnected'))`, identity.OwnerID(ctx), id).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return references.ErrInUse
	}
	if err = remove(); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM reference_images WHERE owner_id=$1 AND id=$2 AND ($3='' OR actor_id::text=$3)`, identity.OwnerID(ctx), id, identity.ContentAuthorFilter(ctx)); err != nil {
		return err
	}
	return tx.Commit()
}
