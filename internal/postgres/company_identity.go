package postgres

import (
	"context"
	"database/sql"
	"errors"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

// Every caller holds the settings lock, before any organization/user row lock.
// This serializes creation and renaming across administrators and all sales ranks.
func checkCompanyIdentity(ctx context.Context, tx *sql.Tx, actor, exclude, name, domain string) error {
	var id string
	var visible bool
	duplicate := org.Duplicate{}
	e := tx.QueryRowContext(ctx, `SELECT o.id,CASE WHEN lower(regexp_replace(btrim(o.name),'\s+',' ','g'))=lower($3) THEN 'name' ELSE 'domain' END,
 COALESCE(NULLIF(u.display_name,''),u.email,'Unassigned'),
 EXISTS(SELECT 1 FROM users actor WHERE actor.id::text=$1 AND actor.is_system_admin)
 OR sales_manages(NULLIF($1,'')::uuid,COALESCE(o.client_manager_id,c.manager_id))
 OR EXISTS(SELECT 1 FROM organization_members m WHERE m.organization_id=o.id AND m.user_id::text=$1 AND m.removed_at IS NULL)
 FROM organizations o LEFT JOIN client_onboardings c ON c.organization_id=o.id LEFT JOIN users u ON u.id=COALESCE(o.client_manager_id,c.manager_id)
 WHERE o.id::text<>$2 AND (lower(regexp_replace(btrim(o.name),'\s+',' ','g'))=lower($3) OR ($4<>'' AND o.domain=$4))
 ORDER BY o.created_at,o.id LIMIT 1`, actor, exclude, name, domain).Scan(&id, &duplicate.Criterion, &duplicate.Manager, &visible)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if visible {
		duplicate.OrganizationID = id
	}
	return &duplicate
}
