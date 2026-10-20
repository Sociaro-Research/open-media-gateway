package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

const clientColumns = `o.id,o.name,c.country,c.domain,c.contact_name,c.email,c.comment,
 CASE WHEN c.activated_at IS NOT NULL THEN 'accepted' WHEN i.revoked_at IS NOT NULL THEN 'revoked'
 WHEN i.expires_at<=NOW() THEN 'expired' ELSE COALESCE(d.status,'send_failed') END,c.version,i.expires_at,c.activated_at`
const clientTables = ` FROM client_onboardings c JOIN organizations o ON o.id=c.organization_id JOIN organization_invites i ON i.id=c.invite_id
 LEFT JOIN LATERAL (SELECT status FROM owner_invitation_deliveries WHERE invite_id=i.id ORDER BY created_at DESC,id DESC LIMIT 1) d ON TRUE`
const clientScope = `EXISTS(SELECT 1 FROM users WHERE id=$1 AND is_client_manager) AND (CASE WHEN c.activated_at IS NULL THEN c.manager_id ELSE o.client_manager_id END)=$1`

func scanClient(row interface{ Scan(...any) error }) (org.Client, error) {
	var c org.Client
	e := row.Scan(&c.OrganizationID, &c.Name, &c.Country, &c.Domain, &c.ContactName, &c.Email, &c.Comment, &c.Status, &c.Version, &c.ExpiresAt, &c.ActivatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		e = org.ErrForbidden
	}
	return c, e
}
func readClient(ctx context.Context, q rowQuerier, actor, id string) (org.Client, error) {
	return scanClient(q.QueryRowContext(ctx, `SELECT `+clientColumns+clientTables+` WHERE c.organization_id=$2 AND `+clientScope, actor, id))
}
func (r *SystemRepository) ClientOnboardings(ctx context.Context, actor string) ([]org.Client, error) {
	manager, e := r.IsClientManager(ctx, actor)
	if e != nil {
		return nil, e
	}
	if !manager || identity.APIKeyID(ctx) != nil {
		return nil, org.ErrForbidden
	}
	rows, e := r.db.QueryContext(ctx, `SELECT `+clientColumns+clientTables+` WHERE `+clientScope+` ORDER BY c.created_at DESC,c.organization_id DESC LIMIT 100`, actor)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []org.Client{}
	for rows.Next() {
		c, e := scanClient(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	return items, rows.Err()
}
func onboardingActor(ctx context.Context, tx *sql.Tx, actor string) error {
	if identity.APIKeyID(ctx) != nil {
		return org.ErrForbidden
	}
	// Same lock order as system-role changes and assignment changes.
	if _, e := readSystemSettings(ctx, tx, true); e != nil {
		return e
	}
	var manager bool
	if e := tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1`, actor).Scan(&manager); e != nil {
		return e
	}
	if !manager {
		return org.ErrForbidden
	}
	return setAuditActor(ctx, tx, actor)
}
func onboardingReplay(ctx context.Context, tx *sql.Tx, actor, operation string, hash []byte) (string, error) {
	var id string
	var same bool
	e := tx.QueryRowContext(ctx, `SELECT organization_id,fingerprint=$3 FROM client_onboarding_operations WHERE actor_id=$1 AND operation_id=$2`, actor, operation, hash).Scan(&id, &same)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if !same {
		return "", org.ErrConflict
	}
	return id, nil
}
func onboardingQuota(ctx context.Context, tx *sql.Tx, actor, id string) error {
	var count int
	var touched bool
	e := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT organization_id),COALESCE(bool_or(organization_id::text=$2),FALSE) FROM client_onboarding_operations WHERE actor_id=$1 AND created_at>NOW()-INTERVAL '1 hour'`, actor, id).Scan(&count, &touched)
	if e == nil && count >= 20 && !touched {
		return org.ErrOnboardingLimit
	}
	return e
}
func onboardingOperation(ctx context.Context, tx *sql.Tx, actor, operation, id string, hash []byte) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO client_onboarding_operations(actor_id,operation_id,organization_id,fingerprint) VALUES($1,$2,$3,$4)`, actor, operation, id, hash)
	return e
}
func ownerDelivery(ctx context.Context, tx *sql.Tx, invite string, d org.OwnerDelivery) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO owner_invitation_deliveries(id,invite_id,organization_id,token_hash,recipient,company_name,origin,expires_at,created_at)
 SELECT $1,i.id,i.organization_id,$3,i.recipient_email,o.name,$4,$5,clock_timestamp() FROM organization_invites i JOIN organizations o ON o.id=i.organization_id WHERE i.id=$2`, d.ID, invite, d.Hash, d.Origin, d.ExpiresAt)
	return e
}
func (r *SystemRepository) CreateClient(ctx context.Context, actor string, in org.ClientInput, d org.OwnerDelivery) (org.Client, error) {
	if e := in.Normalize(); e != nil {
		return org.Client{}, e
	}
	data, _ := json.Marshal(in)
	hash := sha256.Sum256(data)
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return org.Client{}, e
	}
	defer tx.Rollback()
	if e = onboardingActor(ctx, tx, actor); e != nil {
		return org.Client{}, e
	}
	id, e := onboardingReplay(ctx, tx, actor, in.OperationID, hash[:])
	if e != nil {
		return org.Client{}, e
	}
	if id != "" {
		return readClient(ctx, tx, actor, id)
	}
	if e = checkCompanyIdentity(ctx, tx, actor, "", in.Name, in.Domain); e != nil {
		return org.Client{}, e
	}
	id = uuid.NewString()
	if e = onboardingQuota(ctx, tx, actor, id); e != nil {
		return org.Client{}, e
	}
	invite := uuid.NewString()
	if _, e = tx.ExecContext(ctx, `INSERT INTO organizations(id,name,domain) VALUES($1,$2,$3)`, id, in.Name, in.Domain); e != nil {
		return org.Client{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO organization_invites(id,organization_id,token_hash,created_by,expires_at,recipient_email,first_owner,role) VALUES($1,$2,$3,$4,$5,$6,TRUE,'supermanager')`, invite, id, d.Hash, actor, d.ExpiresAt, in.Email); e != nil {
		return org.Client{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO client_onboardings(organization_id,manager_id,country,domain,contact_name,email,comment,invite_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, actor, in.Country, in.Domain, in.ContactName, in.Email, in.Comment, invite); e != nil {
		return org.Client{}, e
	}
	if e = ownerDelivery(ctx, tx, invite, d); e != nil {
		return org.Client{}, e
	}
	if e = onboardingOperation(ctx, tx, actor, in.OperationID, id, hash[:]); e != nil {
		return org.Client{}, e
	}
	c, e := readClient(ctx, tx, actor, id)
	if e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (r *SystemRepository) ChangeOwnerInvitation(ctx context.Context, actor, id, operation string, version int64, revoke bool, d org.OwnerDelivery) (org.Client, error) {
	data, _ := json.Marshal([]any{id, version, revoke})
	hash := sha256.Sum256(data)
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return org.Client{}, e
	}
	defer tx.Rollback()
	if e = onboardingActor(ctx, tx, actor); e != nil {
		return org.Client{}, e
	}
	replay, e := onboardingReplay(ctx, tx, actor, operation, hash[:])
	if e != nil {
		return org.Client{}, e
	}
	if replay != "" {
		return readClient(ctx, tx, actor, replay)
	}
	c, e := readClient(ctx, tx, actor, id)
	if e != nil {
		return c, e
	}
	if c.Version != version {
		return c, org.ErrConflict
	}
	if c.ActivatedAt != nil {
		return c, org.ErrInvitationUnavailable
	}
	if e = onboardingQuota(ctx, tx, actor, id); e != nil {
		return c, e
	}
	var invite string
	if e = tx.QueryRowContext(ctx, `SELECT invite_id FROM client_onboardings WHERE organization_id=$1`, id).Scan(&invite); e != nil {
		return c, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE owner_invitation_deliveries SET status='cancelled',lease=NULL WHERE invite_id=$1 AND status IN ('sending','send_failed')`, invite); e != nil {
		return c, e
	}
	if revoke {
		_, e = tx.ExecContext(ctx, `UPDATE organization_invites SET revoked_at=COALESCE(revoked_at,NOW()),previous_token_hash=NULL,previous_token_expires_at=NULL WHERE id=$1`, invite)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE organization_invites SET previous_token_hash=CASE WHEN revoked_at IS NULL THEN COALESCE(previous_token_hash,token_hash) END,previous_token_expires_at=CASE WHEN revoked_at IS NULL THEN COALESCE(previous_token_expires_at,expires_at) END,token_hash=$2,expires_at=$3,revoked_at=NULL WHERE id=$1`, invite, d.Hash, d.ExpiresAt)
		if e == nil {
			e = ownerDelivery(ctx, tx, invite, d)
		}
	}
	if e != nil {
		return c, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE client_onboardings SET version=version+1 WHERE organization_id=$1`, id); e != nil {
		return c, e
	}
	if e = onboardingOperation(ctx, tx, actor, operation, id, hash[:]); e != nil {
		return c, e
	}
	c, e = readClient(ctx, tx, actor, id)
	if e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (r *SystemRepository) ClaimOwnerDelivery(ctx context.Context) (org.OwnerDelivery, bool, error) {
	d := org.OwnerDelivery{Lease: uuid.NewString()}
	if _, e := r.db.ExecContext(ctx, `UPDATE owner_invitation_deliveries SET status='send_failed',lease=NULL WHERE status='sending' AND created_at<=NOW()-INTERVAL '23 hours'`); e != nil {
		return d, false, e
	}
	// Never retry past the provider's 24-hour idempotency window. A manual resend
	// rotates the token and uses a fresh operation and delivery identifier.
	e := r.db.QueryRowContext(ctx, `WITH due AS (
 SELECT d.id FROM owner_invitation_deliveries d JOIN organization_invites i ON i.id=d.invite_id
 WHERE d.status IN ('sending','send_failed') AND d.next_attempt_at<=NOW() AND d.expires_at>NOW() AND d.created_at>NOW()-INTERVAL '23 hours'
 AND i.accepted_by IS NULL AND i.revoked_at IS NULL AND i.token_hash=d.token_hash
 ORDER BY d.next_attempt_at,d.id LIMIT 1 FOR UPDATE OF d SKIP LOCKED)
 UPDATE owner_invitation_deliveries d SET lease=$1,next_attempt_at=NOW()+INTERVAL '2 minutes',attempts=attempts+1 FROM due WHERE d.id=due.id
 RETURNING d.id,d.invite_id,d.recipient,d.company_name,d.origin,d.expires_at,d.token_hash,d.attempts`, d.Lease).Scan(&d.ID, &d.InviteID, &d.Recipient, &d.Name, &d.Origin, &d.ExpiresAt, &d.Hash, &d.Attempts)
	if errors.Is(e, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, e == nil, e
}
func (r *SystemRepository) FinishOwnerDelivery(ctx context.Context, d org.OwnerDelivery, sent bool) error {
	delay := int64(15 * (1 << min(d.Attempts, 8)))
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE owner_invitation_deliveries SET status=CASE WHEN $3 THEN 'sent' ELSE 'send_failed' END,sent_at=CASE WHEN $3 THEN NOW() ELSE NULL END,next_attempt_at=NOW()+($4*INTERVAL '1 second'),lease=NULL WHERE id=$1 AND lease=$2 AND status IN ('sending','send_failed')`, d.ID, d.Lease, sent, delay)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if sent && n == 1 {
		if _, e = tx.ExecContext(ctx, `UPDATE organization_invites SET previous_token_hash=NULL,previous_token_expires_at=NULL WHERE id=$1 AND token_hash=$2`, d.InviteID, d.Hash); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (r *SystemRepository) firstOwnerInvitation(ctx context.Context, actor, token string, accept bool) (org.Invite, error) {
	i := org.Invite{Role: org.Supermanager, FirstOwner: true}
	if identity.APIKeyID(ctx) != nil {
		return i, org.ErrForbidden
	}
	hash := sha256.Sum256([]byte(token))
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return i, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return i, e
	}
	var recipient, inviteEmail string
	var manager bool
	if e = tx.QueryRowContext(ctx, `SELECT lower(email),is_client_manager FROM users WHERE id=$1 FOR UPDATE`, actor).Scan(&recipient, &manager); e != nil {
		return i, e
	}
	if manager {
		return i, org.ErrForbidden
	}
	var assigned *string
	var activated *time.Time
	e = tx.QueryRowContext(ctx, `SELECT i.id,i.organization_id,o.name,CASE WHEN i.token_hash=$1 THEN i.expires_at ELSE i.previous_token_expires_at END,i.accepted_by,i.revoked_at,i.recipient_email,c.manager_id,c.activated_at FROM organization_invites i JOIN organizations o ON o.id=i.organization_id JOIN client_onboardings c ON c.invite_id=i.id WHERE i.first_owner AND (i.token_hash=$1 OR i.previous_token_hash=$1) FOR UPDATE OF i,o,c`, hash[:]).Scan(&i.ID, &i.OrganizationID, &i.Name, &i.ExpiresAt, &i.AcceptedBy, &i.RevokedAt, &inviteEmail, &assigned, &activated)
	if errors.Is(e, sql.ErrNoRows) {
		return i, org.ErrNotFound
	}
	if e != nil {
		return i, e
	}
	if recipient != inviteEmail {
		return i, org.ErrRecipient
	}
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL)`, i.OrganizationID, actor).Scan(&i.AlreadyMember); e != nil {
		return i, e
	}
	if i.AcceptedBy != nil && *i.AcceptedBy == actor && i.AlreadyMember {
		return i, nil
	}
	if activated != nil || i.AcceptedBy != nil || i.RevokedAt != nil || !i.ExpiresAt.After(time.Now()) {
		return i, org.ErrInvitationUnavailable
	}
	if !accept {
		return i, nil
	}
	var occupied bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND removed_at IS NULL)`, i.OrganizationID).Scan(&occupied); e != nil {
		return i, e
	}
	if occupied {
		return i, org.ErrConflict
	}
	if e = setAuditActor(ctx, tx, actor); e != nil {
		return i, e
	}
	// The first owner controls the organization's member allowances. Inheriting
	// its zero default would block the owner's paid requests even after funding.
	// Organization funds/credit still cap usage; ordinary invites keep the default.
	if _, e = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) VALUES($1,$2,'supermanager',NULL)`, i.OrganizationID, actor); e != nil {
		return i, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE organization_invites SET accepted_by=$2 WHERE id=$1`, i.ID, actor); e != nil {
		return i, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE organizations SET client_manager_id=CASE WHEN client_manager_id IS NULL THEN (SELECT id FROM users WHERE id=$2 AND is_client_manager) ELSE client_manager_id END WHERE id=$1`, i.OrganizationID, assigned); e != nil {
		return i, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE client_onboardings SET activated_at=NOW(),version=version+1 WHERE organization_id=$1 AND activated_at IS NULL`, i.OrganizationID); e != nil {
		return i, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE owner_invitation_deliveries SET status='cancelled',lease=NULL WHERE invite_id=$1 AND status IN ('sending','send_failed')`, i.ID); e != nil {
		return i, e
	}
	i.AcceptedBy = &actor
	return i, tx.Commit()
}

var _ org.OnboardingRepository = (*SystemRepository)(nil)
