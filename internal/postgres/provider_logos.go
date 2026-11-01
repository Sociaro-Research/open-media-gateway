package postgres

import (
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_logos(owner_id, provider_id, data) VALUES ($1,$2,$3)
		ON CONFLICT (owner_id, provider_id) DO UPDATE SET data=EXCLUDED.data`, identity.ProviderOwnerID(ctx), id, data)
	return err
}

func (r *ProviderSettingsRepository) RemoveLogo(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM provider_logos WHERE owner_id=$1 AND provider_id=$2`, identity.ProviderOwnerID(ctx), id)
	return err
}
