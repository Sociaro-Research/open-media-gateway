package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
)

type StorageSettingsRepository struct{ db *sql.DB }

func NewStorageSettingsRepository(db *sql.DB) *StorageSettingsRepository {
	return &StorageSettingsRepository{db: db}
}
func (r *StorageSettingsRepository) Get(ctx context.Context) (storage.SettingsRecord, error) {
	var record storage.SettingsRecord
	err := referenceStorageDB(ctx, r.db).QueryRowContext(ctx, `SELECT s.driver, COALESCE(s.connection_id::text, ''), c.config_ciphertext
 FROM storage_settings s LEFT JOIN storage_connections c ON c.id = s.connection_id AND c.owner_id = s.owner_id
 WHERE s.owner_id = $1`, identity.StorageOwnerID(ctx)).Scan(&record.Driver, &record.ConnectionID, &record.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return record, storage.ErrSettingsNotFound
	}
	if err != nil {
		return record, fmt.Errorf("read storage settings: %w", err)
	}
	return record, nil
}
func (r *StorageSettingsRepository) Connection(ctx context.Context, id string) (storage.SettingsRecord, error) {
	record := storage.SettingsRecord{Driver: "s3", ConnectionID: id}
	err := referenceStorageDB(ctx, r.db).QueryRowContext(ctx, `SELECT config_ciphertext FROM storage_connections c WHERE id=$2 AND (owner_id IN ($1,$3,$4) OR EXISTS(SELECT 1 FROM tasks t WHERE t.owner_id=$1 AND (t.storage_key LIKE 's3:'||c.id::text||'/%' OR t.last_frame_storage_key LIKE 's3:'||c.id::text||'/%')) OR EXISTS(SELECT 1 FROM reference_images r WHERE r.owner_id=$1 AND r.storage_key LIKE 's3:'||c.id::text||'/%') OR EXISTS(SELECT 1 FROM result_objects r WHERE r.owner_id=$1 AND r.deleted_at IS NULL AND r.storage_key LIKE 's3:'||c.id::text||'/%')) LIMIT 1`, identity.OwnerID(ctx), id, identity.SharedOwnerID, identity.StorageOwnerID(ctx)).Scan(&record.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return record, storage.ErrSettingsNotFound
	}
	if err != nil {
		return record, fmt.Errorf("read storage connection: %w", err)
	}
	return record, nil
}
func (r *StorageSettingsRepository) Save(ctx context.Context, record storage.SettingsRecord) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner := identity.StorageOwnerID(ctx)
	if record.ConnectionID != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO storage_connections (owner_id, id, config_ciphertext) VALUES ($1, $2, $3)
  ON CONFLICT (owner_id, id) DO UPDATE SET config_ciphertext = EXCLUDED.config_ciphertext, updated_at = NOW()`, owner, record.ConnectionID, record.Ciphertext)
		if err != nil {
			return fmt.Errorf("save storage connection: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO storage_settings (owner_id, driver, connection_id) VALUES ($1, $2, $3)
 ON CONFLICT (owner_id) DO UPDATE SET driver = EXCLUDED.driver, connection_id = EXCLUDED.connection_id, updated_at = NOW()`, owner, record.Driver, nullableString(record.ConnectionID))
	if err != nil {
		return fmt.Errorf("save storage settings: %w", err)
	}
	return tx.Commit()
}
