package postgres

import (
	"context"
	"database/sql"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
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
	if err == nil {
		if installation.FromContext(ctx).Role == "central" {
			p.shared = true
			p.personal = false
		} else if p.shared {
			p.personal = false
		}
	}
	return
}
func checkAdmissionPolicy(ctx context.Context, tx *sql.Tx, p admissionPolicy) error {
	providerShared := identity.ProviderOwnerID(ctx) == identity.SharedOwnerID
	companyID := identity.OrganizationFromContext(ctx).ID
	storageShared := identity.StorageOwnerID(ctx) == identity.SharedOwnerID || companyID != "" && identity.StorageOwnerID(ctx) == companyID
	if providerShared && !p.shared || !providerShared && !p.personal || storageShared && !p.shared || !storageShared && !p.personal {
		return core.ErrProviderUnavailable
	}
	return nil
}
