package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

func (r *ProviderSettingsRepository) Logo(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT data FROM provider_logos WHERE owner_id=$1 AND provider_id=$2`, identity.ProviderOwnerID(ctx), id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, providerconfig.ErrNotFound
	}
	return data, err
}

func (r *ProviderSettingsRepository) SaveLogo(ctx context.Context, id string, data []byte) error {
	if err := providerconfig.ValidateLogo(data); err != nil {
		return err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, label, company, err := lockProviderLogo(ctx, tx, id)
	if err != nil {
		return err
	}
	if bytes.Equal(before, data) {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_logos(owner_id, provider_id, data) VALUES ($1,$2,$3)
		ON CONFLICT (owner_id, provider_id) DO UPDATE SET data=EXCLUDED.data`, identity.ProviderOwnerID(ctx), id, data)
	if err != nil {
		return err
	}
	action := "provider_logo_added"
	if before != nil {
		action = "provider_logo_replaced"
	}
	if err = providerLogoAudit(ctx, tx, company, id, label, action, before, data); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ProviderSettingsRepository) RemoveLogo(ctx context.Context, id string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, label, company, err := lockProviderLogo(ctx, tx, id)
	if err != nil {
		return err
	}
	if before == nil {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM provider_logos WHERE owner_id=$1 AND provider_id=$2`, identity.ProviderOwnerID(ctx), id); err != nil {
		return err
	}
	if err = providerLogoAudit(ctx, tx, company, id, label, "provider_logo_removed", before, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func lockProviderLogo(ctx context.Context, tx *sql.Tx, id string) (data []byte, label, company string, err error) {
	// The parent exists even before the first upload: its lock serializes absent
	// logo rows as well as replacement/removal, without a read/write race.
	err = tx.QueryRowContext(ctx, `SELECT name,COALESCE((SELECT id::text FROM organizations WHERE id=p.owner_id),'') FROM provider_settings p WHERE owner_id=$1 AND provider_id=$2 FOR UPDATE`, identity.ProviderOwnerID(ctx), id).Scan(&label, &company)
	if errors.Is(err, sql.ErrNoRows) {
		// Removal of an absent logo stays a no-op; insertion still fails its FK.
		return nil, "", "", nil
	}
	if err != nil {
		return nil, "", "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT data FROM provider_logos WHERE owner_id=$1 AND provider_id=$2 FOR UPDATE`, identity.ProviderOwnerID(ctx), id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func providerLogoAudit(ctx context.Context, tx *sql.Tx, company, id, label, action string, before, after []byte) error {
	projected := func(data []byte) any {
		return map[string]any{"configured": data != nil, "size_bytes": len(data)}
	}
	// Kind and owner-prefixed ID preserve the existing provider audit rules for
	// company, personal and installation-shared resources. Never retain artwork.
	return appendNamedAudit(ctx, tx, company, "", action, "provider", identity.ProviderOwnerID(ctx)+"/"+id, label,
		map[string]any{"before": projected(before), "after": projected(after)})
}
