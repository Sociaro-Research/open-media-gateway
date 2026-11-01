package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

const workspaceChallengeColumns = `user_code,client_name,client_origin,COALESCE(replace_key_id::text,''),status,expires_at,COALESCE(actor_id::text,''),COALESCE(organization_id::text,''),COALESCE(key_id::text,''),token_ciphertext`

func scanWorkspaceChallenge(row interface{ Scan(...any) error }) (v wa.Challenge, err error) {
	err = row.Scan(&v.Code, &v.Label, &v.Origin, &v.ReplaceKey, &v.State, &v.Expires, &v.Actor, &v.Company, &v.KeyID, &v.TokenCipher)
	if errors.Is(err, sql.ErrNoRows) {
		err = wa.ErrUnavailable
	}
	return
}
func workspaceHost(ctx context.Context, tx *sql.Tx) error {
	settings, err := readSystemSettings(ctx, tx, true)
	if err != nil {
		return err
	}
	if !settings.CustomerWorkspaces || !settings.SharedResources {
		return wa.ErrDisabled
	}
	return nil
}
func workspaceAccount(ctx context.Context, tx *sql.Tx, actor string) error {
	if identity.APIKeyID(ctx) != nil || actor == "" || actor == identity.SystemOwnerID {
		return wa.ErrForbidden
	}
	var manager bool
	if err := tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id::text=$1`, actor).Scan(&manager); err != nil {
		return wa.ErrForbidden
	}
	if manager {
		return wa.ErrForbidden
	}
	return setAuditActor(ctx, tx, actor)
}
func (r *SystemRepository) AllowAttempt(ctx context.Context, bucket []byte, limit int, window time.Duration) (bool, error) {
	return NewAuthRepository(r.db).AllowEmailAttempt(ctx, bucket, limit, window)
}
func (r *SystemRepository) Start(ctx context.Context, hash []byte, in wa.Challenge) (wa.Challenge, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if err = workspaceHost(ctx, tx); err != nil {
		return in, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET token_ciphertext=NULL WHERE expires_at<=NOW() AND token_ciphertext IS NOT NULL`); err != nil {
		return in, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM workspace_authorizations WHERE expires_at<=NOW() AND key_id IS NULL`); err != nil {
		return in, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_authorizations(device_hash,user_code,client_name,client_origin,replace_key_id,status,expires_at) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,'pending',$6) ON CONFLICT(device_hash) DO NOTHING`, hash, in.Code, in.Label, in.Origin, in.ReplaceKey, in.Expires)
	if err != nil {
		return in, err
	}
	current, err := scanWorkspaceChallenge(tx.QueryRowContext(ctx, `SELECT `+workspaceChallengeColumns+` FROM workspace_authorizations WHERE device_hash=$1`, hash))
	if err != nil {
		return in, err
	}
	if current.Label != in.Label || current.Origin != in.Origin || current.ReplaceKey != in.ReplaceKey {
		return in, wa.ErrConflict
	}
	return current, tx.Commit()
}
func (r *SystemRepository) Describe(ctx context.Context, actor, code string) (wa.Challenge, []wa.Company, bool, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return wa.Challenge{}, nil, false, err
	}
	defer tx.Rollback()
	if err = workspaceHost(ctx, tx); err != nil {
		return wa.Challenge{}, nil, false, err
	}
	if err = workspaceAccount(ctx, tx, actor); err != nil {
		return wa.Challenge{}, nil, false, err
	}
	v, err := scanWorkspaceChallenge(tx.QueryRowContext(ctx, `SELECT `+workspaceChallengeColumns+` FROM workspace_authorizations WHERE user_code=$1 AND expires_at>clock_timestamp()`, wa.NormalizeCode(code)))
	if err != nil {
		return v, nil, false, err
	}
	if v.Actor != "" && v.Actor != actor {
		return wa.Challenge{}, nil, false, wa.ErrForbidden
	}
	var created bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM customer_workspace_owners WHERE user_id=$1)`, actor).Scan(&created); err != nil {
		return v, nil, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.name FROM organizations o JOIN organization_members m ON m.organization_id=o.id WHERE m.user_id=$1 AND m.role='supermanager' AND m.removed_at IS NULL ORDER BY o.created_at,o.id`, actor)
	if err != nil {
		return v, nil, false, err
	}
	companies := []wa.Company{}
	for rows.Next() {
		var company wa.Company
		if err = rows.Scan(&company.ID, &company.Name); err != nil {
			rows.Close()
			return v, nil, false, err
		}
		companies = append(companies, company)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return v, nil, false, err
	}
	return v, companies, !created, tx.Commit()
}
func (r *SystemRepository) Approve(ctx context.Context, actor, code string, in wa.Approval) (wa.Challenge, error) {
	var empty wa.Challenge
	if !in.Deny && !in.Consent {
		return empty, wa.ErrForbidden
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err = workspaceHost(ctx, tx); err != nil {
		return empty, err
	}
	if err = workspaceAccount(ctx, tx, actor); err != nil {
		return empty, err
	}
	current, err := scanWorkspaceChallenge(tx.QueryRowContext(ctx, `SELECT `+workspaceChallengeColumns+` FROM workspace_authorizations WHERE user_code=$1 AND expires_at>clock_timestamp() FOR UPDATE`, wa.NormalizeCode(code)))
	if err != nil {
		return empty, err
	}
	encoded, _ := json.Marshal(in)
	fingerprint := wa.Digest(string(encoded))
	if current.State != "pending" {
		var previous []byte
		if err = tx.QueryRowContext(ctx, `SELECT approval_hash FROM workspace_authorizations WHERE user_code=$1`, current.Code).Scan(&previous); err != nil {
			return empty, err
		}
		if current.Actor == actor && bytes.Equal(previous, fingerprint) {
			return current, tx.Commit()
		}
		return empty, wa.ErrConflict
	}
	if in.Deny {
		_, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET status='denied',actor_id=$2,approval_hash=$3 WHERE user_code=$1`, current.Code, actor, fingerprint)
		if err != nil {
			return empty, err
		}
		current.State = "denied"
		return current, tx.Commit()
	}
	company := in.Company
	if company == "" {
		name := org.CompanyName(in.Name)
		if name == "" || len(name) > 160 {
			return empty, org.ErrInvalid
		}
		domain, e := org.CompanyDomain(in.Website)
		if e != nil {
			return empty, e
		}
		var created bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM customer_workspace_owners WHERE user_id=$1)`, actor).Scan(&created); err != nil {
			return empty, err
		}
		if created {
			return empty, wa.ErrConflict
		}
		if err = checkCompanyIdentity(ctx, tx, actor, "", name, domain); err != nil {
			return empty, err
		}
		company = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO organizations(id,name,domain) VALUES($1,$2,$3)`, company, name, domain); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, actor); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO customer_workspace_owners(user_id,organization_id) VALUES($1,$2)`, actor, company); err != nil {
			return empty, err
		}
	} else {
		var owns bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id::text=$1 AND user_id=$2 AND role='supermanager' AND removed_at IS NULL)`, company, actor).Scan(&owns); err != nil {
			return empty, err
		}
		if !owns {
			return empty, wa.ErrForbidden
		}
	}
	if in.Replace && current.ReplaceKey != "" {
		var own bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_authorizations a JOIN user_api_keys k ON k.id=a.key_id WHERE a.key_id::text=$1 AND a.actor_id=$2 AND k.user_id=$2)`, current.ReplaceKey, actor).Scan(&own); err != nil {
			return empty, err
		}
		if !own {
			return empty, wa.ErrForbidden
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET status='approved',actor_id=$2,organization_id=$3,replace_approved=$4,approval_hash=$5 WHERE user_code=$1`, current.Code, actor, company, in.Replace, fingerprint)
	if err != nil {
		return empty, err
	}
	if err = auditSystem(ctx, tx, actor, "workspace_installation_approved", company, map[string]any{"client_name": current.Label, "client_origin": current.Origin}); err != nil {
		return empty, err
	}
	current.State = "approved"
	current.Actor = actor
	current.Company = company
	return current, tx.Commit()
}
func (r *SystemRepository) Redeem(ctx context.Context, hash []byte, key apikey.Key, tokenHash, encrypted []byte) (wa.Challenge, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return wa.Challenge{}, err
	}
	defer tx.Rollback()
	if err = workspaceHost(ctx, tx); err != nil {
		return wa.Challenge{}, err
	}
	v, err := scanWorkspaceChallenge(tx.QueryRowContext(ctx, `SELECT `+workspaceChallengeColumns+` FROM workspace_authorizations WHERE device_hash=$1 AND expires_at>clock_timestamp() FOR UPDATE`, hash))
	if err != nil {
		return v, err
	}
	var next time.Time
	if err = tx.QueryRowContext(ctx, `SELECT next_poll_at FROM workspace_authorizations WHERE device_hash=$1`, hash).Scan(&next); err != nil {
		return v, err
	}
	if time.Now().Before(next) {
		_, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET next_poll_at=next_poll_at+INTERVAL '5 seconds' WHERE device_hash=$1`, hash)
		if err != nil {
			return v, err
		}
		if err = tx.Commit(); err != nil {
			return v, err
		}
		return v, wa.ErrSlow
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET next_poll_at=NOW()+INTERVAL '5 seconds' WHERE device_hash=$1`, hash); err != nil {
		return v, err
	}
	if v.State == "pending" {
		if err = tx.Commit(); err != nil {
			return v, err
		}
		return v, wa.ErrPending
	}
	if v.State != "approved" && v.State != "issued" {
		return v, wa.ErrUnavailable
	}
	// Revalidate actual membership, even if the browser consent was recent.
	var permitted bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN organization_members m ON m.user_id=u.id WHERE u.id=$1 AND NOT u.is_client_manager AND m.organization_id=$2 AND m.role='supermanager' AND m.removed_at IS NULL)`, v.Actor, v.Company).Scan(&permitted); err != nil {
		return v, err
	}
	if !permitted {
		return v, wa.ErrForbidden
	}
	if v.State == "issued" {
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_api_keys WHERE id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp())`, v.KeyID).Scan(&permitted); err != nil {
			return v, err
		}
		if !permitted {
			return v, wa.ErrUnavailable
		}
		return v, tx.Commit()
	}
	key.UserID = v.Actor
	key.CreatedBy = v.Actor
	key.OrganizationID = v.Company
	key.Name = v.Label
	actorCtx := identity.WithSystemPermission(identity.WithOwnerID(ctx, v.Actor), false)
	if err = createAPIKey(actorCtx, tx, v.Actor, key, tokenHash); err != nil {
		return v, err
	}
	var replace bool
	if err = tx.QueryRowContext(ctx, `SELECT replace_approved FROM workspace_authorizations WHERE device_hash=$1`, hash).Scan(&replace); err != nil {
		return v, err
	}
	if replace && v.ReplaceKey != "" {
		if _, err = tx.ExecContext(actorCtx, `UPDATE user_api_keys SET revoked_at=NOW() WHERE id::text=$1 AND user_id=$2 AND revoked_at IS NULL`, v.ReplaceKey, v.Actor); err != nil {
			return v, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET status='issued',key_id=$2,token_ciphertext=$3 WHERE device_hash=$1`, hash, key.ID, encrypted); err != nil {
		return v, err
	}
	v.State = "issued"
	v.KeyID = key.ID
	v.TokenCipher = encrypted
	return v, tx.Commit()
}
func (r *SystemRepository) Cancel(ctx context.Context, hash []byte) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	v, err := scanWorkspaceChallenge(tx.QueryRowContext(ctx, `SELECT `+workspaceChallengeColumns+` FROM workspace_authorizations WHERE device_hash=$1 FOR UPDATE`, hash))
	if errors.Is(err, wa.ErrUnavailable) {
		// No retained authorization means this host has no key issued by this proof.
		// Keep cancellation idempotent after unissued request cleanup.
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if v.KeyID != "" {
		if err = setAuditActor(ctx, tx, v.Actor); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=NOW() WHERE id=$1 AND revoked_at IS NULL`, v.KeyID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET status='cancelled',token_ciphertext=NULL WHERE device_hash=$1`, hash); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *SystemRepository) Grants(ctx context.Context, actor string) ([]wa.Grant, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT k.id,k.organization_id,k.name,a.client_origin,k.expires_at,k.revoked_at FROM workspace_authorizations a JOIN user_api_keys k ON k.id=a.key_id WHERE a.actor_id=$1 AND k.user_id=$1 ORDER BY k.created_at DESC LIMIT 100`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wa.Grant{}
	for rows.Next() {
		var g wa.Grant
		if err = rows.Scan(&g.ID, &g.Company, &g.Name, &g.ClientOrigin, &g.Expires, &g.Revoked); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (r *SystemRepository) Revoke(ctx context.Context, actor, id string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	if err = workspaceAccount(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE user_api_keys k SET revoked_at=COALESCE(revoked_at,NOW()) WHERE k.id::text=$1 AND k.user_id=$2 AND EXISTS(SELECT 1 FROM workspace_authorizations a WHERE a.key_id=k.id AND a.actor_id=$2)`, id, actor)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return wa.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_authorizations SET token_ciphertext=NULL WHERE key_id::text=$1 AND actor_id=$2`, id, actor); err != nil {
		return err
	}
	return tx.Commit()
}

var _ wa.Repository = (*SystemRepository)(nil)
