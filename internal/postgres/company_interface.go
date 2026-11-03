package postgres

import (
	"context"
	"database/sql"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
)

// CompanyInterfaceAuthority reads central records only. It intentionally exposes
// no provisioning/rotation operation until those transactions are implemented.
type CompanyInterfaceAuthority struct{ db *sql.DB }

func NewCompanyInterfaceAuthority(db *sql.DB) *CompanyInterfaceAuthority {
	return &CompanyInterfaceAuthority{db: db}
}

var _ ci.Authority = (*CompanyInterfaceAuthority)(nil)

func (r *CompanyInterfaceAuthority) Installation(ctx context.Context, hash [32]byte) (ci.Grant, error) {
	var g ci.Grant
	var installation []byte
	err := r.db.QueryRowContext(ctx, `SELECT id::text,installation_hash,organization_id::text,central_origin,approved_local_origin,purpose,protocol_version,revocation_epoch,expires_at,revoked_at IS NOT NULL FROM company_interface_grants WHERE credential_hash=$1`, hash[:]).Scan(&g.ID, &installation, &g.Company, &g.CentralOrigin, &g.LocalOrigin, &g.Purpose, &g.Version, &g.Epoch, &g.ExpiresAt, &g.Revoked)
	if err != nil || len(installation) != 32 {
		return ci.Grant{}, ci.ErrDenied
	}
	copy(g.InstallationHash[:], installation)
	return g, nil
}

func (r *CompanyInterfaceAuthority) Session(ctx context.Context, hash [32]byte) (ci.Session, error) {
	var s ci.Session
	var local []byte
	err := r.db.QueryRowContext(ctx, `SELECT id::text,grant_id::text,organization_id::text,actor_id::text,local_session_hash,grant_epoch,session_epoch,token_version,access_expires_at,revoked_at IS NOT NULL FROM company_interface_sessions WHERE access_hash=$1`, hash[:]).Scan(&s.ID, &s.GrantID, &s.Company, &s.Actor, &local, &s.GrantEpoch, &s.Epoch, &s.Version, &s.ExpiresAt, &s.Revoked)
	if err != nil || len(local) != 32 {
		return ci.Session{}, ci.ErrDenied
	}
	copy(s.LocalSessionHash[:], local)
	return s, nil
}

func (r *CompanyInterfaceAuthority) Membership(ctx context.Context, company, actor string) (string, error) {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL`, company, actor).Scan(&role)
	if err != nil {
		return "", ci.ErrDenied
	}
	return role, nil
}
