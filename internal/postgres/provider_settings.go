package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

type ProviderSettingsRepository struct {
	db *sql.DB
}

func NewProviderSettingsRepository(db *sql.DB) *ProviderSettingsRepository {
	return &ProviderSettingsRepository{db: db}
}

func (r *ProviderSettingsRepository) Get(ctx context.Context, providerID string) (providerconfig.Record, error) {
	var record providerconfig.Record
	err := r.db.QueryRowContext(ctx, `
		SELECT provider_id, enabled, credentials_ciphertext, credential_source, created_at, updated_at, name, archived, version
		FROM provider_settings WHERE owner_id = $1 AND provider_id = $2`, identity.ProviderOwnerID(ctx), providerID).Scan(
		&record.ProviderID, &record.Enabled, &record.CredentialsCiphertext,
		&record.CredentialSource, &record.CreatedAt, &record.UpdatedAt, &record.Name, &record.Archived, &record.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return providerconfig.Record{}, providerconfig.ErrNotFound
	}
	if err != nil {
		return providerconfig.Record{}, fmt.Errorf("get provider setting: %w", err)
	}
	return record, nil
}

func (r *ProviderSettingsRepository) List(ctx context.Context) ([]providerconfig.Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, enabled, credentials_ciphertext, credential_source, created_at, updated_at, name, archived, version
		FROM provider_settings WHERE owner_id = $1 ORDER BY provider_id`, identity.ProviderOwnerID(ctx))
	if err != nil {
		return nil, fmt.Errorf("list provider settings: %w", err)
	}
	defer rows.Close()
	records := make([]providerconfig.Record, 0)
	for rows.Next() {
		var record providerconfig.Record
		if err := rows.Scan(
			&record.ProviderID, &record.Enabled, &record.CredentialsCiphertext,
			&record.CredentialSource, &record.CreatedAt, &record.UpdatedAt, &record.Name, &record.Archived, &record.Version,
		); err != nil {
			return nil, fmt.Errorf("scan provider setting: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *ProviderSettingsRepository) Upsert(ctx context.Context, record providerconfig.Record) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO provider_settings (
			owner_id, provider_id, enabled, credentials_ciphertext, credential_source, created_at, updated_at, name, archived
		) VALUES ($1, $2, $3, $4, $5, NOW(), NOW(), $6, $7)
		ON CONFLICT (owner_id, provider_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			credentials_ciphertext = EXCLUDED.credentials_ciphertext,
			credential_source = EXCLUDED.credential_source,
			updated_at = NOW(), name = EXCLUDED.name, archived = EXCLUDED.archived, version = provider_settings.version + 1
        WHERE $8 = 0 OR provider_settings.version = $8`,
		identity.ProviderOwnerID(ctx), record.ProviderID, record.Enabled, nullableBytes(record.CredentialsCiphertext), record.CredentialSource, record.Name, record.Archived, record.Version,
	)
	if err != nil {
		return fmt.Errorf("upsert provider setting: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("provider settings changed; reload before saving")
	}
	return tx.Commit()
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (r *ProviderSettingsRepository) SaveRevision(ctx context.Context, id, fingerprint string, ciphertext []byte) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_setting_revisions(owner_id, provider_id, fingerprint, credentials_ciphertext) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, identity.ProviderOwnerID(ctx), id, fingerprint, ciphertext)
	return err
}
func (r *ProviderSettingsRepository) GetRevision(ctx context.Context, id, fingerprint string) ([]byte, error) {
	var value []byte
	err := r.db.QueryRowContext(ctx, `SELECT credentials_ciphertext FROM provider_setting_revisions WHERE owner_id=$1 AND provider_id=$2 AND fingerprint=$3`, identity.ProviderOwnerID(ctx), id, fingerprint).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, providerconfig.ErrNotFound
	}
	return value, err
}
