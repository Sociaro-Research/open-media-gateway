package postgres

import (
	"context"
	"database/sql"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type admissionPolicy struct{ shared, personal bool }

// Lock before inserting work, in the same order as settings updates. A profile
// disable cannot race an old HTTP policy check and admit work after its commit.
func lockAdmissionPolicy(ctx context.Context, tx *sql.Tx) (p admissionPolicy, err error) {
	if !system.RequiresAdmissionCheck(ctx) {
		return admissionPolicy{shared: true, personal: true}, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT shared_resources,personal_providers FROM system_settings WHERE id=TRUE FOR SHARE`).Scan(&p.shared, &p.personal)
	return
}
func checkAdmissionPolicy(ctx context.Context, tx *sql.Tx, p admissionPolicy) error {
	if identity.ProviderOwnerID(ctx) == identity.SharedOwnerID || identity.StorageOwnerID(ctx) == identity.SharedOwnerID {
		if !p.shared {
			return core.ErrProviderUnavailable
		}
		return nil
	}
	if p.personal || identity.ActorID(ctx) == identity.SystemOwnerID || identity.ActorID(ctx) == "" {
		return nil
	}
	var admin bool
	if err := tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id::text=$1`, identity.ActorID(ctx)).Scan(&admin); err != nil {
		return err
	}
	if !admin {
		return core.ErrProviderUnavailable
	}
	return nil
}
