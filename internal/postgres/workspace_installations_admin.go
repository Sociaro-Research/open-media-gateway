package postgres

import (
	"context"
	"database/sql"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
)

// Serialize the current administrator decision with role changes and existing
// workspace issue/revoke transactions. Browser authentication is also checked at
// the HTTP boundary; key credentials never authorize these repository operations.
func installationAdministrator(ctx context.Context, tx *sql.Tx, actor string) error {
	if err := workspaceHost(ctx, tx); err != nil {
		return err
	}
	if actor == "" || actor == identity.SystemOwnerID || identity.APIKeyID(ctx) != nil || identity.CompanyBoundary(ctx) != "" || !identity.SystemPermissionAllowed(ctx) {
		return wa.ErrForbidden
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT is_system_admin AND NOT is_client_manager FROM users WHERE id::text=$1 FOR SHARE`, actor).Scan(&allowed); err != nil {
		return wa.ErrForbidden
	}
	if !allowed {
		return wa.ErrForbidden
	}
	return setAuditActor(ctx, tx, actor)
}

// A single bounded-shape overview avoids one request per organization row.
func (r *SystemRepository) InstallationAccessSummary(ctx context.Context, actor string) ([]wa.InstallationAccessCount, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = installationAdministrator(ctx, tx, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.organization_id,count(*) FROM user_api_keys k
 WHERE k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
 AND EXISTS(SELECT 1 FROM workspace_authorizations a WHERE a.key_id=k.id AND a.organization_id=k.organization_id)
 GROUP BY k.organization_id ORDER BY k.organization_id`)
	if err != nil {
		return nil, err
	}
	counts := []wa.InstallationAccessCount{}
	for rows.Next() {
		var count wa.InstallationAccessCount
		if err = rows.Scan(&count.Company, &count.Active); err != nil {
			rows.Close()
			return nil, err
		}
		counts = append(counts, count)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return counts, tx.Commit()
}

func (r *SystemRepository) OrganizationInstallationGrants(ctx context.Context, actor, company string) ([]wa.InstallationGrant, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = installationAdministrator(ctx, tx, actor); err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id::text=$1)`, company).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, wa.ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.id,k.organization_id,k.name,a.client_origin,k.expires_at,k.revoked_at,o.name,
 CASE WHEN a.installation_hash IS NULL THEN 'legacy' ELSE 'dedicated' END,a.access_intent,k.created_at,k.prefix
 FROM workspace_authorizations a JOIN user_api_keys k ON k.id=a.key_id AND k.organization_id=a.organization_id
 JOIN organizations o ON o.id=k.organization_id
 WHERE k.organization_id::text=$1 ORDER BY k.created_at DESC,k.id`, company)
	if err != nil {
		return nil, err
	}
	grants := []wa.InstallationGrant{}
	for rows.Next() {
		var g wa.InstallationGrant
		if err = rows.Scan(&g.ID, &g.Company, &g.Name, &g.ClientOrigin, &g.Expires, &g.Revoked, &g.CompanyName, &g.Provisioning, &g.Access, &g.CreatedAt, &g.Prefix); err != nil {
			rows.Close()
			return nil, err
		}
		grants = append(grants, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return grants, tx.Commit()
}

func (r *SystemRepository) RevokeOrganizationInstallation(ctx context.Context, actor, company, id string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = installationAdministrator(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE user_api_keys k SET revoked_at=COALESCE(revoked_at,NOW())
 WHERE k.id::text=$1 AND k.organization_id::text=$2
 AND EXISTS(SELECT 1 FROM workspace_authorizations a WHERE a.key_id=k.id AND a.organization_id=k.organization_id)`, id, company)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return wa.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET token_ciphertext=NULL WHERE key_id::text=$1 AND organization_id::text=$2`, id, company); err != nil {
		return err
	}
	return tx.Commit()
}

var _ wa.InstallationAdministration = (*SystemRepository)(nil)
