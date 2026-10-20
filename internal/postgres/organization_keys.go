package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
)

func (r *APIKeyRepository) OrganizationKeys(ctx context.Context, actor, organization string) ([]apikey.Key, error) {
	role, err := organizationAccess(ctx, r.db, actor, organization)
	if err != nil {
		return nil, apikey.ErrForbidden
	}
	rows, err := r.db.QueryContext(ctx, `SELECT k.id,k.name,k.prefix,k.scopes,k.created_at,k.expires_at,k.last_used_at,k.revoked_at,k.resource_mode,k.storage_mode,k.organization_id,k.user_id,k.created_by,u.email,c.email,m.role FROM user_api_keys k JOIN users u ON u.id=k.user_id JOIN users c ON c.id=k.created_by JOIN organization_members m ON m.organization_id=k.organization_id AND m.user_id=k.user_id WHERE k.organization_id=$1 ORDER BY k.created_at DESC,k.id`, organization)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []apikey.Key{}
	for rows.Next() {
		var k apikey.Key
		var scopes []byte
		var recipientRole string
		if err = rows.Scan(&k.ID, &k.Name, &k.Prefix, &scopes, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt, &k.ResourceMode, &k.StorageMode, &k.OrganizationID, &k.UserID, &k.CreatedBy, &k.UserEmail, &k.CreatorEmail, &recipientRole); err != nil {
			return nil, err
		}
		if !org.Statistics(role) && k.UserID != actor && !org.IssueKey(role, actor, recipientRole, k.UserID) {
			continue
		}
		if err = json.Unmarshal(scopes, &k.Scopes); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *APIKeyRepository) RevokeForActor(ctx context.Context, actor, id string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner, organization string
	if err = tx.QueryRowContext(ctx, `SELECT user_id,COALESCE(organization_id::text,'') FROM user_api_keys WHERE id=$1`, id).Scan(&owner, &organization); errors.Is(err, sql.ErrNoRows) {
		return apikey.ErrNotFound
	} else if err != nil {
		return err
	}
	if organization == "" {
		if owner != actor {
			return apikey.ErrForbidden
		}
	} else {
		var ignored string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, organization).Scan(&ignored); err != nil {
			return err
		}
		role, e := organizationAccess(ctx, tx, actor, organization)
		if e != nil {
			return apikey.ErrForbidden
		}
		var targetRole string
		if err = tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2`, organization, owner).Scan(&targetRole); err != nil {
			return err
		}
		if !org.IssueKey(role, actor, targetRole, owner) {
			return apikey.ErrForbidden
		}
	}
	if err = setAuditActor(ctx, tx, actor); err != nil {
		return err
	}
	if err = setAuditRevocationReason(ctx, tx, "explicit"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=COALESCE(revoked_at,NOW()) WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
