package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"strings"
	"time"
)

const weekStartSQL = `(date_trunc('week',NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`

func organizationAccess(ctx context.Context, q rowQuerier, actor, id string) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, `SELECT CASE WHEN u.is_client_manager THEN CASE WHEN EXISTS(SELECT 1 FROM organizations WHERE id=$2 AND sales_manages(u.id,client_manager_id)) THEN 'client_manager' ELSE '' END WHEN u.is_system_admin AND $3 THEN 'system_admin' ELSE COALESCE(m.role,'') END FROM users u LEFT JOIN organization_members m ON m.user_id=u.id AND m.organization_id=$2 AND m.removed_at IS NULL WHERE u.id=$1 AND EXISTS(SELECT 1 FROM organizations WHERE id=$2)`, actor, id, identity.SystemPermissionAllowed(ctx)).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && role == "" {
		return "", org.ErrForbidden
	}
	return role, err
}
func (r *SystemRepository) OrganizationAccess(ctx context.Context, actor, id string) (string, error) {
	return organizationAccess(ctx, r.db, actor, id)
}
func orgLock(ctx context.Context, tx *sql.Tx, actor, id string) (string, error) {
	var ignored string
	if e := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&ignored); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", org.ErrNotFound
		}
		return "", e
	}
	if e := setAuditActor(ctx, tx, actor); e != nil {
		return "", e
	}
	role, e := organizationAccess(ctx, tx, actor, id)
	if e == nil && !org.Manage(role) {
		return "", org.ErrForbidden
	}
	return role, e
}
func (r *SystemRepository) Organizations(ctx context.Context, actor string, all bool) ([]org.Organization, error) {
	return r.organizations(ctx, actor, all, nil, nil)
}

func (r *SystemRepository) OrganizationsWithUsage(ctx context.Context, actor string, all bool, from, until time.Time) ([]org.Organization, error) {
	if !from.Before(until) {
		return nil, org.ErrInvalid
	}
	return r.organizations(ctx, actor, all, &from, &until)
}

func (r *SystemRepository) organizations(ctx context.Context, actor string, all bool, from, until *time.Time) ([]org.Organization, error) {
	all = all && identity.SystemPermissionAllowed(ctx)
	columns, join := "", ""
	args := []any{actor, all}
	if from != nil {
		columns = `,usage.spend,usage.unknown_costs,usage.last_activity`
		join = ` LEFT JOIN LATERAL (
 SELECT COALESCE(SUM(CEIL((` + taskConsumedSQL + `)*1000000)) FILTER (WHERE created_at >= $3 AND created_at < $4),0)::bigint AS spend,
 COUNT(*) FILTER (WHERE created_at >= $3 AND created_at < $4 AND status NOT IN ('queued','running') AND (` + taskConsumedSQL + `) IS NULL) AS unknown_costs,
 MAX(created_at) AS last_activity FROM tasks WHERE owner_id=o.id
 ) usage ON TRUE`
		args = append(args, *from, *until)
	}
	rows, e := r.db.QueryContext(ctx, `SELECT o.id,o.name,o.domain,CASE WHEN u.is_client_manager THEN 'client_manager' ELSE COALESCE(m.role,CASE WHEN $2 AND u.is_system_admin THEN 'system_admin' ELSE '' END) END,o.credit_limit_micros,o.default_weekly_limit_micros,o.created_at,m.weekly_limit_micros,
 COALESCE((SELECT SUM(amount_micros) FROM organization_credits WHERE organization_id=o.id),0)::bigint,
 COALESCE((SELECT SUM(CEIL((`+taskConsumedSQL+`)*1000000)) FROM tasks WHERE owner_id=o.id AND created_at>=o.created_at AND provider_owner_id='00000000-0000-0000-0000-000000000001'),0)::bigint,
 COALESCE((SELECT SUM(CEIL((`+taskConsumedSQL+`)*1000000)) FROM tasks WHERE owner_id=o.id AND actor_id=$1 AND created_at>=`+weekStartSQL+` AND created_at>=o.created_at AND provider_owner_id='00000000-0000-0000-0000-000000000001'),0)::bigint,
 COALESCE((SELECT SUM(size_bytes) FROM stored_objects WHERE owner_id=o.id AND deleted_at IS NULL),0)::bigint,o.client_manager_id,COALESCE((SELECT email FROM users WHERE id=o.client_manager_id),''),o.markup_basis_points,COALESCE(o.markup_basis_points,(SELECT markup_basis_points FROM system_settings WHERE id=TRUE)),u.is_system_admin,o.billing_profile,o.minimize_request_logging
 `+columns+` FROM organizations o JOIN users u ON u.id=$1 LEFT JOIN organization_members m ON m.organization_id=o.id AND m.user_id=$1 AND m.removed_at IS NULL`+join+` WHERE (u.is_client_manager AND sales_manages(u.id,o.client_manager_id)) OR (NOT u.is_client_manager AND (m.user_id IS NOT NULL OR ($2 AND u.is_system_admin))) ORDER BY o.created_at,o.id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []org.Organization{}
	for rows.Next() {
		var o org.Organization
		var credits, consumed money.USD
		var isSystemAdmin bool
		var billingProfile []byte
		values := []any{&o.ID, &o.Name, &o.Domain, &o.Role, &o.CreditLimit, &o.DefaultWeeklyLimit, &o.CreatedAt, &o.WeeklyLimit, &credits, &consumed, &o.WeeklyCommitted, &o.StorageBytes, &o.ClientManagerID, &o.ClientManagerEmail, &o.MarkupPercent, &o.EffectiveMarkupPercent, &isSystemAdmin, &billingProfile, &o.MinimizeRequestLogging}
		if from != nil {
			o.Usage = &org.UsageSummary{}
			values = append(values, &o.Usage.Spend, &o.Usage.UnknownCosts, &o.Usage.LastActivity)
		}
		if e = rows.Scan(values...); e != nil {
			return nil, e
		}
		if len(billingProfile) > 0 && org.Statistics(o.Role) {
			if e = json.Unmarshal(billingProfile, &o.BillingProfile); e != nil {
				return nil, e
			}
		}
		if !isSystemAdmin || !identity.SystemPermissionAllowed(ctx) {
			o.MarkupPercent = nil
			o.EffectiveMarkupPercent = nil
		}
		o.Balance = credits - consumed
		o.Available = credits + o.CreditLimit - consumed
		out = append(out, o)
	}
	return out, rows.Err()
}
func (r *SystemRepository) CreateOrganization(ctx context.Context, actor, name, firstAdmin, website string) (org.Organization, error) {
	o := org.Organization{ID: uuid.NewString(), Name: org.CompanyName(name)}
	if o.Name == "" || len(o.Name) > 160 {
		return o, org.ErrInvalid
	}
	domain, e := org.CompanyDomain(website)
	if e != nil {
		return o, e
	}
	o.Domain = domain
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return o, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return o, e
	}
	if _, e = systemActor(ctx, tx, actor); e != nil {
		return o, e
	}
	if e = checkCompanyIdentity(ctx, tx, actor, "", o.Name, o.Domain); e != nil {
		return o, e
	}
	var user string
	if firstAdmin = strings.TrimSpace(firstAdmin); firstAdmin != "" {
		if e = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE (id::text=$1 OR lower(email)=lower($1)) AND NOT is_client_manager ORDER BY id LIMIT 1 FOR UPDATE`, firstAdmin).Scan(&user); e != nil {
			return o, org.ErrNotFound
		}
	}
	if e = tx.QueryRowContext(ctx, `INSERT INTO organizations(id,name,domain) VALUES($1,$2,$3) RETURNING created_at`, o.ID, o.Name, o.Domain).Scan(&o.CreatedAt); e != nil {
		return o, e
	}
	if user != "" {
		if _, e = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, o.ID, user); e != nil {
			return o, e
		}
	}
	zero := money.USD(0)
	o.DefaultWeeklyLimit = &zero
	o.Role = "system_admin"
	if actor == user {
		o.Role = "supermanager"
		o.WeeklyLimit = &zero
	}
	return o, tx.Commit()
}
func (r *SystemRepository) OrganizationSettings(ctx context.Context, actor, id, name string, credit money.USD, weekly *money.USD, changeCredit bool, markup ...*money.Percent) error {
	if len(markup) > 1 || len(markup) == 1 && markup[0] != nil && markup[0].Validate() != nil {
		return org.ErrInvalid
	}
	if strings.TrimSpace(name) == "" || len(name) > 160 || credit < 0 || credit > money.USD(1_000_000*money.Scale) || weekly != nil && (*weekly < 0 || *weekly > money.USD(1_000_000*money.Scale)) {
		return org.ErrInvalid
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return e
	}
	role, e := orgLock(ctx, tx, actor, id)
	if e != nil {
		return e
	}
	if (changeCredit || len(markup) > 0) && role != "system_admin" {
		return org.ErrForbidden
	}
	name = org.CompanyName(name)
	var previous string
	if e = tx.QueryRowContext(ctx, `SELECT name FROM organizations WHERE id=$1`, id).Scan(&previous); e != nil {
		return e
	}
	if name != org.CompanyName(previous) {
		if e = checkCompanyIdentity(ctx, tx, actor, id, name, ""); e != nil {
			return e
		}
	}
	var rate *money.Percent
	if len(markup) > 0 {
		rate = markup[0]
	}
	_, e = tx.ExecContext(ctx, `UPDATE organizations SET name=$2,default_weekly_limit_micros=$3,credit_limit_micros=CASE WHEN $4 THEN $5 ELSE credit_limit_micros END,markup_basis_points=CASE WHEN $6 THEN $7 ELSE markup_basis_points END,markup_version=markup_version+CASE WHEN $6 THEN 1 ELSE 0 END WHERE id=$1`, id, strings.TrimSpace(name), weekly, changeCredit, credit, len(markup) > 0, rate)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// SetOrganizationRequestLogging changes future requests only. Accepted tasks
// retain the setting captured at admission so an in-flight job is not changed.
func (r *SystemRepository) SetOrganizationRequestLogging(ctx context.Context, actor, id string, minimize bool) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = orgLock(ctx, tx, actor, id); err != nil {
		return err
	}
	var previous bool
	if err = tx.QueryRowContext(ctx, `SELECT minimize_request_logging FROM organizations WHERE id=$1`, id).Scan(&previous); err != nil {
		return err
	}
	if previous == minimize {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organizations SET minimize_request_logging=$2 WHERE id=$1`, id, minimize); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *SystemRepository) OrganizationMembers(ctx context.Context, actor, id string) ([]org.Member, error) {
	role, e := r.OrganizationAccess(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	if role == org.MemberRole {
		return nil, org.ErrForbidden
	}
	rows, e := r.db.QueryContext(ctx, `SELECT u.id,u.display_name,u.email,COALESCE(u.picture_url,''),u.contacts,m.role,m.weekly_limit_micros,COALESCE((SELECT SUM(CEIL((`+taskConsumedSQL+`)*1000000)) FROM tasks WHERE owner_id=m.organization_id AND actor_id=m.user_id AND created_at>=`+weekStartSQL+` AND created_at>=(SELECT created_at FROM organizations WHERE id=$1) AND provider_owner_id='00000000-0000-0000-0000-000000000001'),0)::bigint, COALESCE((SELECT SUM(CEIL((`+taskConsumedSQL+` )*1000000)) FROM tasks WHERE owner_id=m.organization_id AND actor_id=m.user_id),0)::bigint FROM organization_members m JOIN users u ON u.id=m.user_id WHERE m.organization_id=$1 AND m.removed_at IS NULL ORDER BY m.role,u.email`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []org.Member{}
	for rows.Next() {
		var m org.Member
		var contacts []byte
		if e = rows.Scan(&m.ID, &m.Name, &m.Email, &m.Picture, &contacts, &m.Role, &m.WeeklyLimit, &m.WeeklyCommitted, &m.TotalConsumed); e != nil {
			return nil, e
		}
		if err := json.Unmarshal(contacts, &m.Contacts); err != nil {
			return nil, err
		}
		if role == org.Developer && m.ID != actor {
			m.HideFinancial = true
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *SystemRepository) SetOrganizationMember(ctx context.Context, actor, id, target, role string, limit *money.USD, limitProvided, remove bool) error {
	if !org.ValidRole(role) || limit != nil && (*limit < 0 || *limit > money.USD(1_000_000*money.Scale)) {
		return org.ErrInvalid
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	access, e := orgLock(ctx, tx, actor, id)
	if e != nil {
		return e
	}
	var uid string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE (id::text=$1 OR lower(email)=lower($1)) AND NOT is_client_manager ORDER BY id LIMIT 1 FOR UPDATE`, target).Scan(&uid); e != nil {
		return org.ErrNotFound
	}
	var previous string
	var previousLimit *money.USD
	e = tx.QueryRowContext(ctx, `SELECT role,weekly_limit_micros FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL`, id, uid).Scan(&previous, &previousLimit)
	if errors.Is(e, sql.ErrNoRows) {
		if access != "system_admin" || remove {
			return org.ErrNotFound
		}
	} else if e != nil {
		return e
	}
	if !org.MutateMember(access, previous, role) && !(access == org.Manager && previous == org.Manager && role == org.Manager && !remove) {
		return org.ErrForbidden
	}
	// Resolve omitted limits under the organization lock: inherit for new or
	// restored members, and preserve an active member's individual limit.
	if !remove && !limitProvided {
		limit = previousLimit
		if previous == "" {
			if e = tx.QueryRowContext(ctx, `SELECT default_weekly_limit_micros FROM organizations WHERE id=$1`, id).Scan(&limit); e != nil {
				return e
			}
		}
	}
	if previous == "supermanager" && (remove || role != "supermanager") {
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_members WHERE organization_id=$1 AND role='supermanager' AND removed_at IS NULL`, id).Scan(&count); e != nil {
			return e
		}
		if count <= 1 {
			return org.ErrConflict
		}
	}
	if remove {
		if e = setAuditRevocationReason(ctx, tx, "membership_removed"); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE organization_members SET removed_at=COALESCE(removed_at,NOW()) WHERE organization_id=$1 AND user_id=$2`, id, uid)
		if e == nil {
			_, e = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=COALESCE(revoked_at,NOW()) WHERE organization_id=$1 AND user_id=$2`, id, uid)
		}
	} else {
		_, e = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,user_id) DO UPDATE SET role=$3,weekly_limit_micros=$4,removed_at=NULL`, id, uid, role, limit)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *SystemRepository) OrganizationCredits(ctx context.Context, actor, id string, offset int) ([]org.Credit, error) {
	role, e := r.OrganizationAccess(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	if !org.Statistics(role) {
		return nil, org.ErrForbidden
	}
	rows, e := r.db.QueryContext(ctx, `SELECT id,actor_id,amount_micros,kind,reason,external_id,created_at FROM organization_credits WHERE organization_id=$1 ORDER BY created_at DESC,id LIMIT 51 OFFSET $2`, id, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []org.Credit{}
	for rows.Next() {
		var c org.Credit
		if e = rows.Scan(&c.ID, &c.Actor, &c.Amount, &c.Kind, &c.Reason, &c.ExternalID, &c.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *SystemRepository) AddOrganizationCredit(ctx context.Context, actor, id string, c org.Credit) (org.Credit, error) {
	if c.Amount == 0 || c.Amount < -money.USD(1_000_000*money.Scale) || c.Kind != "payment" && c.Kind != "promo" && c.Kind != "adjustment" || c.Amount < 0 && c.Kind != "adjustment" || len(c.Reason) > 1000 || strings.TrimSpace(c.ExternalID) == "" || len(c.ExternalID) > 200 {
		return c, org.ErrInvalid
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return c, e
	}
	e = commercialPermission(ctx, tx, actor, id, "topup")
	if e != nil {
		if errors.Is(e, system.ErrForbidden) {
			return c, org.ErrForbidden
		}
		return c, e
	}
	var admin bool
	if e = tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id=$1`, actor).Scan(&admin); e != nil {
		return c, e
	}
	if !admin && (c.Amount <= 0 || c.Kind != "payment" || strings.TrimSpace(c.Reason) == "") {
		return c, org.ErrInvalid
	}
	var locked string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, id).Scan(&locked); e != nil {
		return c, e
	}
	var old org.Credit
	e = tx.QueryRowContext(ctx, `SELECT id,actor_id,amount_micros,kind,reason,external_id,created_at FROM organization_credits WHERE organization_id=$1 AND external_id=$2`, id, c.ExternalID).Scan(&old.ID, &old.Actor, &old.Amount, &old.Kind, &old.Reason, &old.ExternalID, &old.CreatedAt)
	if e == nil {
		if old.Amount != c.Amount || old.Kind != c.Kind || old.Reason != c.Reason {
			return c, org.ErrConflict
		}
		return old, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return c, e
	}
	c.ID = uuid.NewString()
	c.Actor = actor
	e = tx.QueryRowContext(ctx, `INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,reason,external_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, c.ID, id, actor, c.Amount, c.Kind, c.Reason, c.ExternalID).Scan(&c.CreatedAt)
	if e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (r *SystemRepository) OrganizationInvites(ctx context.Context, actor, id string) ([]org.Invite, error) {
	role, e := r.OrganizationAccess(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	if !org.Manage(role) {
		return nil, org.ErrForbidden
	}
	rows, e := r.db.QueryContext(ctx, `SELECT i.id,i.organization_id,i.expires_at,i.accepted_by,i.revoked_at,COALESCE(u.email,''),i.role,i.first_owner FROM organization_invites i LEFT JOIN users u ON u.id=i.accepted_by WHERE i.organization_id=$1 ORDER BY i.created_at DESC LIMIT 100`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []org.Invite{}
	for rows.Next() {
		var i org.Invite
		if e = rows.Scan(&i.ID, &i.OrganizationID, &i.ExpiresAt, &i.AcceptedBy, &i.RevokedAt, &i.AcceptedByEmail, &i.Role, &i.FirstOwner); e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (r *SystemRepository) CreateOrganizationInvite(ctx context.Context, actor, id string, roles ...string) (org.Invite, error) {
	role := org.MemberRole
	if len(roles) > 1 {
		return org.Invite{}, org.ErrInvalid
	}
	if len(roles) == 1 {
		role = roles[0]
	}
	if !org.ValidRole(role) {
		return org.Invite{}, org.ErrInvalid
	}
	i := org.Invite{ID: uuid.NewString(), OrganizationID: id, Role: role, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
	var secret [32]byte
	if _, e := rand.Read(secret[:]); e != nil {
		return i, e
	}
	i.Token = base64.RawURLEncoding.EncodeToString(secret[:])
	hash := sha256.Sum256([]byte(i.Token))
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return i, e
	}
	defer tx.Rollback()
	access, e := orgLock(ctx, tx, actor, id)
	if e != nil {
		return i, e
	}
	if !org.MutateMember(access, org.MemberRole, role) {
		return i, org.ErrForbidden
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO organization_invites(id,organization_id,token_hash,created_by,expires_at,role) VALUES($1,$2,$3,$4,$5,$6)`, i.ID, id, hash[:], actor, i.ExpiresAt, role)
	if e != nil {
		return i, e
	}
	return i, tx.Commit()
}
func (r *SystemRepository) RevokeOrganizationInvite(ctx context.Context, actor, id, invite string) error {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = orgLock(ctx, tx, actor, id); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE organization_invites SET revoked_at=COALESCE(revoked_at,NOW()) WHERE id=$1 AND organization_id=$2`, invite, id)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *SystemRepository) OrganizationInvitation(ctx context.Context, actor, token string, accept bool) (org.Invite, error) {
	i := org.Invite{}
	if len(token) != 43 {
		return i, org.ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	var firstOwner bool
	if e := r.db.QueryRowContext(ctx, `SELECT first_owner FROM organization_invites WHERE token_hash=$1 OR previous_token_hash=$1`, hash[:]).Scan(&firstOwner); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return i, e
	}
	if firstOwner {
		return r.firstOwnerInvitation(ctx, actor, token, accept)
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return i, e
	}
	defer tx.Rollback()
	var manager bool
	if e = tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1 FOR UPDATE`, actor).Scan(&manager); e != nil {
		return i, e
	}
	if manager {
		return i, org.ErrForbidden
	}
	e = tx.QueryRowContext(ctx, `SELECT i.id,i.organization_id,o.name,i.expires_at,i.accepted_by,i.revoked_at,i.role FROM organization_invites i JOIN organizations o ON o.id=i.organization_id WHERE i.token_hash=$1`, hash[:]).Scan(&i.ID, &i.OrganizationID, &i.Name, &i.ExpiresAt, &i.AcceptedBy, &i.RevokedAt, &i.Role)
	if errors.Is(e, sql.ErrNoRows) {
		return i, org.ErrNotFound
	}
	if e != nil {
		return i, e
	}
	// A current member can use an old invitation as a link to their organization.
	// This is read-only: never restore removed members or consume another invite.
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL)`, i.OrganizationID, actor).Scan(&i.AlreadyMember); e != nil {
		return i, e
	}
	if i.AlreadyMember {
		return i, nil
	}
	if i.RevokedAt != nil || !i.ExpiresAt.After(time.Now()) || i.AcceptedBy != nil {
		return i, org.ErrInvitationUnavailable
	}
	if !accept {
		return i, nil
	}
	if e = setAuditActor(ctx, tx, actor); e != nil {
		return i, e
	}
	var ignored string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, i.OrganizationID).Scan(&ignored); e != nil {
		return i, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE organization_invites SET accepted_by=$2 WHERE id=$1 AND accepted_by IS NULL AND revoked_at IS NULL AND expires_at>NOW()`, i.ID, actor)
	if e != nil {
		return i, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return i, org.ErrInvitationUnavailable
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) SELECT id,$2,$3,default_weekly_limit_micros FROM organizations WHERE id=$1 ON CONFLICT(organization_id,user_id) DO UPDATE SET removed_at=NULL,role=CASE WHEN organization_members.removed_at IS NULL THEN organization_members.role ELSE EXCLUDED.role END,weekly_limit_micros=CASE WHEN organization_members.removed_at IS NULL THEN organization_members.weekly_limit_micros ELSE EXCLUDED.weekly_limit_micros END`, i.OrganizationID, actor, i.Role)
	if e != nil {
		return i, e
	}
	return i, tx.Commit()
}
