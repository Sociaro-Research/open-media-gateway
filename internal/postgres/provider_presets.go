package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

var _ providerresources.PresetImporter = (*ProviderResourceRepository)(nil)

// ImportPreset never creates a native job. The existing partial unique index
// fences concurrent imports and preserves the owner/connection/revision identity.
func (r *ProviderResourceRepository) ImportPreset(ctx context.Context, b providerresources.Binding) (providerresources.Binding, error) {
	if err := providerresources.ValidatePresetBinding(ctx, b); err != nil {
		return providerresources.Binding{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return providerresources.Binding{}, err
	}
	defer tx.Rollback()
	saved, err := scanResource(tx.QueryRowContext(ctx, `INSERT INTO provider_resources(id,owner_id,actor_id,provider_id,connection_id,provider_owner_id,revision,remote_id,kind,subtype,name,status,is_preset) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'succeeded',true) ON CONFLICT(owner_id,provider_owner_id,provider_id,connection_id,revision,kind,remote_id) WHERE status<>'deleted' DO NOTHING RETURNING `+resourceColumns,
		b.ID, b.OwnerID, b.ActorID, b.ProviderID, b.ConnectionID, b.ProviderOwnerID, b.Revision, b.RemoteID, b.Kind, b.Subtype, b.Name))
	if errors.Is(err, providerresources.ErrNotFound) {
		saved, err = scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM provider_resources WHERE owner_id=$1 AND provider_owner_id=$2 AND provider_id=$3 AND connection_id=$4 AND revision=$5 AND kind=$6 AND remote_id=$7 AND status<>'deleted' FOR SHARE`, b.OwnerID, b.ProviderOwnerID, b.ProviderID, b.ConnectionID, b.Revision, b.Kind, b.RemoteID))
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, providerresources.ErrNotFound) {
			return providerresources.Binding{}, providerresources.ErrConflict
		}
	}
	if err != nil {
		return providerresources.Binding{}, err
	}
	// Never turn an existing custom, failed or renamed resource into a preset.
	if !saved.IsPreset || saved.Status != providerresources.Succeeded || saved.Subtype != b.Subtype || saved.Name != b.Name || saved.Description != "" || saved.BoundVoiceBindingID != "" || saved.OriginTaskID != "" || saved.Session != nil || len(saved.Sources) != 0 {
		return providerresources.Binding{}, providerresources.ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return providerresources.Binding{}, err
	}
	return saved, nil
}
