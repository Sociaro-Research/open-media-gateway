package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
)

type TemplateRepository struct {
	db     *sql.DB
	lockMu sync.Mutex
	locks  *sql.DB
	closed bool
}

func NewTemplateRepository(db *sql.DB) *TemplateRepository { return &TemplateRepository{db: db} }

// Publication transactions hold row locks while callbacks use ordinary
// repositories/admission. Isolate those locks from the downstream pool, and
// bound BOTH readers and waiting editors so neither can consume its capacity.
func (r *TemplateRepository) publicationDB(ctx context.Context) (*sql.DB, error) {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	if r.closed {
		return nil, sql.ErrConnDone
	}
	if r.locks != nil {
		return r.locks, nil
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		native, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("template publication requires the PostgreSQL driver")
		}
		// Copy the actual connector configuration, including configured schema
		// and TLS parameters. Credentials are not separately configured or exposed.
		r.locks = sql.OpenDB(stdlib.GetConnector(*native.Conn().Config().Copy()))
		r.locks.SetMaxOpenConns(2)
		r.locks.SetMaxIdleConns(2)
		r.locks.SetConnMaxIdleTime(5 * time.Minute)
		r.locks.SetConnMaxLifetime(30 * time.Minute)
		return nil
	})
	return r.locks, err
}

func (r *TemplateRepository) Close() error {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	r.closed = true
	if r.locks != nil {
		return r.locks.Close()
	}
	return nil
}

const templateColumns = `id,version,revision,content,is_published,updated_at`

func scanTemplate(row rowScanner) (templates.Record, error) {
	var value templates.Record
	var content []byte
	var published bool
	err := row.Scan(&value.ID, &value.Version, &value.Revision, &content, &published, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, templates.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if err = json.Unmarshal(content, &value.Draft); err != nil {
		return value, err
	}
	if published {
		value.Published = &value.Draft
	}
	return value, err
}
func (r *TemplateRepository) List(ctx context.Context, admin bool) ([]templates.Record, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+templateColumns+` FROM generation_templates WHERE removed_at IS NULL AND ($1 OR is_published) ORDER BY updated_at DESC,id`, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []templates.Record{}
	for rows.Next() {
		v, e := scanTemplate(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (r *TemplateRepository) Get(ctx context.Context, id string) (templates.Record, error) {
	if _, e := uuid.Parse(id); e != nil {
		return templates.Record{}, templates.ErrNotFound
	}
	return scanTemplate(r.db.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM generation_templates WHERE id=$1 AND removed_at IS NULL`, id))
}
func (r *TemplateRepository) Save(ctx context.Context, input templates.Record, expected int64, action string) (templates.Record, error) {
	if expected == 0 && action != "" {
		return input, templates.ErrInvalid
	}
	if action != "" && action != "publish" && action != "unpublish" && action != "remove" {
		return input, templates.ErrInvalid
	}
	locks, err := r.publicationDB(ctx)
	if err != nil {
		return input, err
	}
	tx, err := locks.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer tx.Rollback()
	// Serialize create retries and edits of one identity before row/global locks.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(199,hashtext($1))`, input.ID); err != nil {
		return input, err
	}
	// Wait for in-flight admissions before acquiring the asset lock. Holding
	// the asset lock while waiting for this row could deadlock downstream reads.
	var current templates.Record
	if expected > 0 {
		current, err = scanTemplate(tx.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM generation_templates WHERE id=$1 AND removed_at IS NULL FOR UPDATE`, input.ID))
		if err != nil {
			return input, err
		}
		if current.Version != expected {
			return input, templates.ErrConflict
		}
		if action == "unpublish" || action == "remove" {
			input.Draft = current.Draft
		}
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(199079)`); err != nil {
		return input, err
	}
	ids := []string{input.Draft.CoverID, input.Draft.OverrideSoundID}
	for _, slot := range input.Draft.Slots {
		ids = append(ids, slot.AssetID)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE NOT deleting AND owner_id=$1 AND id::text=$2)`, templates.LibraryOwnerID, id).Scan(&exists); err != nil {
			return input, err
		}
		if !exists {
			return input, templates.ErrNotFound
		}
	}
	if expected == 0 {
		draft, e := json.Marshal(input.Draft)
		if e != nil {
			return input, e
		}
		result, e := tx.ExecContext(ctx, `INSERT INTO generation_templates(id,content) VALUES($1,$2) ON CONFLICT DO NOTHING`, input.ID, draft)
		if e != nil {
			return input, e
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return input, templates.ErrConflict
		}
	} else {
		draft, e := json.Marshal(input.Draft)
		if e != nil {
			return input, e
		}
		published := current.Published != nil
		if action == "publish" {
			published = true
		}
		if action == "unpublish" || action == "remove" {
			published = false
		}
		revision := current.Revision
		if published {
			for _, id := range ids {
				if id != "" {
					if _, e := tx.ExecContext(ctx, `UPDATE reference_images SET template_published=true WHERE owner_id=$1 AND id::text=$2`, templates.LibraryOwnerID, id); e != nil {
						return input, e
					}
				}
			}

			revision = expected + 1
		}
		_, err = tx.ExecContext(ctx, `UPDATE generation_templates SET version=version+1,revision=$3,content=$4,is_published=$5,updated_at=now(),removed_at=CASE WHEN $6 THEN now() ELSE NULL END WHERE id=$1 AND version=$2`, input.ID, expected, revision, draft, published, action == "remove")
		if err != nil {
			return input, err
		}
	}
	result, err := scanTemplate(tx.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM generation_templates WHERE id=$1`, input.ID))
	if err != nil {
		return input, err
	}
	if err = tx.Commit(); err != nil {
		return input, err
	}
	return result, nil
}
func (r *TemplateRepository) Asset(ctx context.Context, id string) (templates.Asset, error) {
	var a templates.Asset
	if _, e := uuid.Parse(id); e != nil {
		return a, templates.ErrNotFound
	}
	e := r.db.QueryRowContext(ctx, `SELECT r.id,'',r.content_type,r.size_bytes,r.storage_key,COALESCE(r.content_sha256,''),
 r.template_published OR COALESCE((SELECT bool_or(published) FROM template_media WHERE id=r.id),false) OR EXISTS(SELECT 1 FROM generation_templates t WHERE t.is_published AND (t.content->>'cover_id'=r.id::text OR t.content->>'override_sound_id'=r.id::text OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(t.content->'slots','null'::jsonb),'[]')) s WHERE s->>'asset_id'=r.id::text))),r.name,r.id,COALESCE(r.storage_owner_id::text,r.owner_id::text)
 FROM reference_images r WHERE NOT r.deleting AND r.owner_id=$2 AND r.id=$1`, id, templates.LibraryOwnerID).Scan(&a.ID, &a.TemplateID, &a.ContentType, &a.SizeBytes, &a.StorageKey, &a.SHA256, &a.Published, &a.Name, &a.LibraryReferenceID, &a.StorageOwnerID)
	if errors.Is(e, sql.ErrNoRows) {
		e = templates.ErrNotFound
	}
	return a, e
}

// PutAsset registers canonical metadata without ever replacing existing bytes.
func (r *TemplateRepository) PutAsset(ctx context.Context, a templates.Asset) error {
	if a.LibraryReferenceID != a.ID {
		_, err := r.db.ExecContext(ctx, `INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,content_sha256) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(owner_id,id) DO NOTHING`, templates.LibraryOwnerID, a.ID, a.Name, a.ContentType, a.SizeBytes, a.StorageKey, a.SHA256)
		if err != nil {
			return err
		}
	}
	existing, err := r.Asset(ctx, a.ID)
	if err != nil {
		return err
	}
	if existing.StorageKey != a.StorageKey || existing.SHA256 != a.SHA256 || existing.ContentType != a.ContentType || existing.SizeBytes != a.SizeBytes {
		return templates.ErrConflict
	}
	return nil
}

// Hold publication stable through admission. Editors wait for in-flight
// admission; unpublish cannot race a new task past the publication check.
func (r *TemplateRepository) WithPublished(ctx context.Context, id string, revision int64, use func(templates.Record) error) error {
	if _, err := uuid.Parse(id); err != nil {
		return templates.ErrNotFound
	}
	locks, err := r.publicationDB(ctx)
	if err != nil {
		return err
	}
	tx, err := locks.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	item, err := scanTemplate(tx.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM generation_templates WHERE id=$1 AND removed_at IS NULL FOR SHARE`, id))
	if err != nil {
		return err
	}
	if item.Published == nil {
		return templates.ErrNotFound
	}
	if item.Revision != revision {
		return templates.ErrConflict
	}
	return use(item)
}
