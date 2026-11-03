package postgres

import (
	"context"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
)

// The revision snapshot and subsequent exact check prevent an SDK request from
// crossing a global rebind. Decryption remains installation-wide server custody.
func (r *CompanyInterfaceLifecycle) LocalInstallationSnapshot(ctx context.Context) (ci.InstallationSnapshot, error) {
	var out ci.InstallationSnapshot
	v, e := r.LocalInstallation(ctx)
	if e != nil {
		return out, e
	}
	out.Installation = v
	e = r.db.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE AND grant_id=$1 AND grant_epoch=$2 AND organization_id=$3 AND central_origin=$4 AND approved_local_origin=$5`, v.GrantID, v.GrantEpoch, v.Company, v.CentralOrigin, v.LocalOrigin).Scan(&out.Revision)
	if e != nil {
		return ci.InstallationSnapshot{}, ci.ErrConflict
	}
	return out, nil
}
func (r *CompanyInterfaceLifecycle) CheckLocalInstallation(ctx context.Context, v ci.InstallationSnapshot) error {
	i := v.Installation
	var current bool
	e := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM company_interface_local_control WHERE singleton=TRUE AND revision=$1 AND grant_id=$2 AND grant_epoch=$3 AND organization_id=$4 AND central_origin=$5 AND approved_local_origin=$6 AND grant_ciphertext IS NOT NULL)`, v.Revision, i.GrantID, i.GrantEpoch, i.Company, i.CentralOrigin, i.LocalOrigin).Scan(&current)
	if e != nil {
		return e
	}
	if !current {
		return ci.ErrConflict
	}
	return nil
}

var _ ci.CustomerStore = (*CompanyInterfaceLifecycle)(nil)
