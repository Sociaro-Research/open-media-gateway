package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/google/uuid"
)

type ProviderAssetRepository struct{ db *sql.DB }

func NewProviderAssetRepository(db *sql.DB) *ProviderAssetRepository {
	return &ProviderAssetRepository{db}
}

const assetColumns = `id,owner_id::text,actor_id,provider_owner_id::text,provider,library_scope,source_id,asset_id,status,data,updated_at`

func scanAsset(row interface{ Scan(...any) error }) (providerassets.Record, error) {
	var r providerassets.Record
	var raw []byte
	var id, status string
	err := row.Scan(&r.ID, &r.Owner, &r.Actor, &r.ProviderOwner, &r.Provider, &r.Scope, &r.SourceID, &id, &status, &raw, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, providerassets.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r.Asset); err != nil {
		return r, err
	}
	r.Asset.ID = id
	r.Status = status
	return r, nil
}
func (r *ProviderAssetRepository) List(ctx context.Context, p, scope string, offset int) ([]providerassets.Record, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+assetColumns+` FROM provider_assets WHERE owner_id=$1 AND provider_owner_id=$2 AND provider=$3 AND library_scope=$4 AND status NOT IN ('Deleted','Detached') AND ($5='' OR actor_id=$5) ORDER BY updated_at DESC,id LIMIT 49 OFFSET $6`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), p, scope, identity.ContentAuthorFilter(ctx), offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerassets.Record{}
	for rows.Next() {
		item, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (r *ProviderAssetRepository) Get(ctx context.Context, id string) (providerassets.Record, error) {
	if _, err := uuid.Parse(id); err != nil {
		return providerassets.Record{}, providerassets.ErrNotFound
	}
	return scanAsset(r.db.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM provider_assets WHERE id=$1 AND owner_id=$2 AND provider_owner_id=$3 AND ($4='' OR actor_id=$4)`, id, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), identity.ContentAuthorFilter(ctx)))
}
func (r *ProviderAssetRepository) Reserve(ctx context.Context, a providerassets.Record) (providerassets.Record, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return a, false, err
	}
	defer tx.Rollback()
	if a.SourceID != "" {
		if err = sourceAssetLock(ctx, tx, identity.OwnerID(ctx), a.SourceID); err != nil {
			return a, false, err
		}
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE owner_id=$1 AND id=$2)`, identity.OwnerID(ctx), a.SourceID).Scan(&exists)
		if err != nil {
			return a, false, err
		}
		if !exists {
			return a, false, providerassets.ErrNotFound
		}
	}
	if a.Asset.ID != "" {
		if err = assetLock(ctx, tx, identity.ProviderOwnerID(ctx), a.Provider, a.Asset.ID); err != nil {
			return a, false, err
		}
		var deleted bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_assets WHERE provider_owner_id=$1 AND provider=$2 AND library_scope=$3 AND asset_id=$4 AND status IN ('Deleting','Deleted'))`, identity.ProviderOwnerID(ctx), a.Provider, a.Scope, a.Asset.ID).Scan(&deleted)
		if err != nil {
			return a, false, err
		}
		if deleted {
			return a, false, providerassets.ErrConflict
		}
	}
	saved := a.Asset
	saved.PreviewURL = ""
	raw, _ := json.Marshal(saved)
	result, err := tx.ExecContext(ctx, `INSERT INTO provider_assets(id,owner_id,actor_id,provider_owner_id,provider,library_scope,source_id,asset_id,status,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, a.ID, identity.OwnerID(ctx), identity.ActorID(ctx), identity.ProviderOwnerID(ctx), a.Provider, a.Scope, a.SourceID, a.Asset.ID, a.Status, raw)
	if err != nil {
		return a, false, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		a, err = scanAsset(tx.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM provider_assets WHERE owner_id=$1 AND provider_owner_id=$2 AND provider=$3 AND library_scope=$4 AND status NOT IN ('Deleted','Detached') AND (($5<>'' AND source_id=$5) OR ($6<>'' AND asset_id=$6)) LIMIT 1`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), a.Provider, a.Scope, a.SourceID, a.Asset.ID))
		if err != nil {
			return a, false, err
		}
	}
	return a, n == 1, tx.Commit()
}
func (r *ProviderAssetRepository) Save(ctx context.Context, a providerassets.Record) error {
	saved := a.Asset
	saved.PreviewURL = ""
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE provider_assets SET asset_id=$1,status=$2,data=$3,updated_at=now() WHERE id=$4 AND owner_id=$5 AND provider_owner_id=$6 AND provider=$7 AND library_scope=$8 AND (status NOT IN ('Deleting','Deleted','Detached') OR $2 IN ('Deleting','Deleted','Detached')) AND (asset_id='' OR ($1<>'' AND asset_id=$1)) AND (status<>'Active' OR $2 NOT IN ('Creating','Unknown','Processing'))`, a.Asset.ID, a.Status, raw, a.ID, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), a.Provider, a.Scope)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return providerassets.ErrConflict
	}
	return nil
}
func (r *ProviderAssetRepository) Pending(ctx context.Context, limit int) ([]providerassets.Record, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+assetColumns+` FROM provider_assets WHERE status IN ('Processing','Creating','Unknown') ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerassets.Record{}
	for rows.Next() {
		a, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func assetLock(ctx context.Context, tx *sql.Tx, owner, provider, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, owner+"/"+provider+"/"+id)
	return err
}
func (r *ProviderAssetRepository) BeginDelete(ctx context.Context, id string) (providerassets.Record, error) {
	a, err := r.Get(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Asset.ID == "" {
		if a.Status != "Failed" {
			return a, providerassets.ErrConflict
		}
		a.Status = "Deleted"
		return a, r.Save(ctx, a)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	if err = assetLock(ctx, tx, a.ProviderOwner, a.Provider, a.Asset.ID); err != nil {
		return a, err
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE provider_owner_id=$1 AND provider=$2 AND status IN ('queued','running') AND jsonb_path_exists(request_options,'$.** ? (@ == $asset)',jsonb_build_object('asset',$3::text))) OR EXISTS(SELECT 1 FROM provider_assets WHERE provider_owner_id=$1 AND provider=$2 AND library_scope=$4 AND asset_id=$5 AND owner_id<>$6 AND status NOT IN ('Deleted','Detached'))`, a.ProviderOwner, a.Provider, "asset://"+a.Asset.ID, a.Scope, a.Asset.ID, identity.OwnerID(ctx)).Scan(&busy)
	if err != nil {
		return a, err
	}
	if busy {
		return a, providerassets.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_assets SET status='Deleting',updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return a, err
	}
	a.Status = "Deleting"
	return a, tx.Commit()
}
func (r *ProviderAssetRepository) Groups(ctx context.Context, p, scope string) ([]providerassets.Group, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name FROM provider_asset_groups WHERE owner_id=$1 AND provider_owner_id=$2 AND provider=$3 AND library_scope=$4 ORDER BY name,id`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), p, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerassets.Group{}
	for rows.Next() {
		var g providerassets.Group
		if err = rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (r *ProviderAssetRepository) SaveGroup(ctx context.Context, p, scope string, g providerassets.Group) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_asset_groups(owner_id,provider_owner_id,provider,library_scope,id,name) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), p, scope, g.ID, g.Name)
	return err
}

// Shares locks with deletion: either admission sees a tombstone, or deletion
// sees the queued task. An idempotent replay is checked after its existing row.
func checkProviderAssets(ctx context.Context, tx *sql.Tx, task core.Task) error {
	refs := append([]string{}, task.Options.ImageURLs...)
	refs = append(refs, task.Options.Videos()...)
	refs = append(refs, task.Options.Audios()...)
	for _, v := range task.Options.ReferenceVoices {
		refs = append(refs, v)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		if !core.IsAssetReference(ref) {
			continue
		}
		id := strings.TrimPrefix(ref, "asset://")
		owner := resourceOwner(task.ProviderOwnerID, identity.OwnerID(ctx))
		if err := assetLock(ctx, tx, owner, task.Provider, id); err != nil {
			return err
		}
		var deleted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_assets WHERE provider_owner_id=$1 AND provider=$2 AND asset_id=$3 AND (status IN ('Deleting','Deleted') OR (owner_id=$4 AND status NOT IN ('Active','Detached'))))`, owner, task.Provider, id, identity.OwnerID(ctx)).Scan(&deleted); err != nil {
			return err
		}
		if deleted {
			return core.ErrOptionsInvalid
		}
	}
	return nil
}

func (r *ProviderAssetRepository) Detach(ctx context.Context, id string) error {
	a, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assetLock(ctx, tx, a.ProviderOwner, a.Provider, a.Asset.ID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE provider_assets SET status='Detached',updated_at=now() WHERE id=$1 AND status NOT IN ('Creating','Unknown','Processing','Deleting','Deleted','Detached')`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return providerassets.ErrConflict
	}
	return tx.Commit()
}

// Serializes source deletion with registration reservation before remote ingestion.
func sourceAssetLock(ctx context.Context, tx *sql.Tx, owner, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "provider-asset-source/"+owner+"/"+id)
	return err
}
