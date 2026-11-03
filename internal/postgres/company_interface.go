package postgres

import (
	"context"
	"database/sql"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"

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
	err := r.db.QueryRowContext(ctx, `SELECT s.id::text,s.grant_id::text,s.organization_id::text,s.actor_id::text,s.local_session_hash,s.grant_epoch,s.session_epoch,s.token_version,LEAST(s.access_expires_at,a.expires_at),s.revoked_at IS NOT NULL OR a.revoked_at IS NOT NULL FROM company_interface_sessions s JOIN company_interface_source_sessions a ON a.id=s.source_session_id AND a.actor_id=s.actor_id WHERE s.access_hash=$1`, hash[:]).Scan(&s.ID, &s.GrantID, &s.Company, &s.Actor, &local, &s.GrantEpoch, &s.Epoch, &s.Version, &s.ExpiresAt, &s.Revoked)
	if err != nil || len(local) != 32 {
		return ci.Session{}, ci.ErrDenied
	}
	copy(s.LocalSessionHash[:], local)
	return s, nil
}

func (r *CompanyInterfaceAuthority) Membership(ctx context.Context, company, actor string) (string, error) {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT m.role FROM organization_members m JOIN users u ON u.id=m.user_id WHERE m.organization_id=$1 AND m.user_id=$2 AND m.removed_at IS NULL AND NOT u.is_client_manager`, company, actor).Scan(&role)
	if err != nil {
		return "", ci.ErrDenied
	}
	return role, nil
}

// AuthenticateCompanyKey uses the original direct-central bearer validator.
// The actual recipient is user_id, never key.created_by or a local key record.
func (r *CompanyInterfaceAuthority) AuthenticateCompanyKey(ctx context.Context, secret, company string) (ci.KeyIdentity, error) {
	user, key, err := NewAPIKeyRepository(r.db).Authenticate(ctx, apikey.Hash(secret))
	if err != nil || key.OrganizationID != company {
		return ci.KeyIdentity{}, ci.ErrDenied
	}
	return ci.KeyIdentity{ID: key.ID, Company: company, Subject: user.ID, Scopes: append([]string(nil), key.Scopes...)}, nil
}

// Direct-central bearer USE has no creator/recipient hierarchy or actor equality
// restriction. Both the verified browser actor and key subject must be current
// members; key Authenticate separately enforces expiry/revocation/scopes.
func (r *CompanyInterfaceAuthority) AuthorizeKeyRecipient(ctx context.Context, company, browser, recipient string) error {
	if _, err := r.Membership(ctx, company, browser); err != nil {
		return ci.ErrDenied
	}
	if _, err := r.Membership(ctx, company, recipient); err != nil {
		return ci.ErrDenied
	}
	return nil
}

func (r *CompanyInterfaceAuthority) User(ctx context.Context, actor string) (identity.User, error) {
	if !ci.ValidID(actor) {
		return identity.User{}, ci.ErrDenied
	}
	return scanProfile(r.db.QueryRowContext(ctx, `SELECT id,email,display_name,COALESCE(picture_url,''),theme,report_columns,color_preset,contacts FROM users WHERE id=$1 AND NOT is_client_manager`, actor))
}

var _ ci.CustomerAuthority = (*CompanyInterfaceAuthority)(nil)
