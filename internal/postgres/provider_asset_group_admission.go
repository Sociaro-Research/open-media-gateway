package postgres

import (
	"context"
	"encoding/json"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
)

func (r *ProviderAssetRepository) AdmitGroup(ctx context.Context, id, provider, scope, name string) error {
	a, ok := ci.MutationAuthorityFromContext(ctx)
	if !ok || a.Operation != "asset-group-create" || !ci.ValidID(id) {
		return ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guardCompanyMutation(ctx, tx, identity.OwnerID(ctx)); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO provider_asset_group_admissions(id,owner_id,actor_id,provider_owner_id,provider,library_scope,name,grant_id,session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid)`, id, identity.OwnerID(ctx), identity.ActorID(ctx), identity.ProviderOwnerID(ctx), provider, scope, name, a.Original.Grant.ID, a.Original.SessionID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// Complete only the already admitted operation. Revocation prevents new intents,
// not saving the result of a provider call that began under a committed intent.
func (r *ProviderAssetRepository) CompleteGroup(ctx context.Context, id, provider, scope string, g providerassets.Group) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	var prior []byte
	e = tx.QueryRowContext(ctx, `SELECT state,result FROM provider_asset_group_admissions WHERE id=$1 AND owner_id=$2 AND actor_id=$3 AND provider_owner_id=$4 AND provider=$5 AND library_scope=$6 FOR UPDATE`, id, identity.OwnerID(ctx), identity.ActorID(ctx), identity.ProviderOwnerID(ctx), provider, scope).Scan(&state, &prior)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(g)
	if e != nil {
		return e
	}
	if state == "completed" {
		var old providerassets.Group
		if json.Unmarshal(prior, &old) != nil || old != g {
			return providerassets.ErrConflict
		}
		return tx.Commit()
	}
	if g.ID == "" {
		return providerassets.ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO provider_asset_groups(owner_id,provider_owner_id,provider,library_scope,id,name) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), provider, scope, g.ID, g.Name); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE provider_asset_group_admissions SET state='completed',result=$2,completed_at=clock_timestamp() WHERE id=$1`, id, raw); e != nil {
		return e
	}
	return tx.Commit()
}
