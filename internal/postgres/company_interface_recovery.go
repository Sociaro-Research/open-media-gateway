package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

// liveIssued rejects revoked or superseded receipts; recovery never restores a
// prior token version or bypasses current membership/source-session validity.
func (r *CompanyInterfaceLifecycle) liveIssued(ctx context.Context, tx *sql.Tx, a ciAttempt) (ci.Grant, error) {
	g, e := ciReadGrant(ctx, tx, a.grant)
	if e != nil || g.Validate(time.Now()) != nil || g.CentralOrigin != r.central || g.InstallationHash != a.binding.InstallationHash || g.LocalOrigin != a.binding.LocalOrigin {
		return g, ci.ErrDenied
	}
	if _, e = ciRole(ctx, tx, g.Company, a.actor); e != nil {
		return g, e
	}
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM company_interface_sessions s JOIN company_interface_source_sessions a ON a.id=s.source_session_id AND a.actor_id=s.actor_id WHERE s.id=$1 AND s.grant_id=$2 AND s.organization_id=$3 AND s.actor_id=$4 AND s.source_session_id=$5 AND s.grant_epoch=$6 AND s.local_session_hash=$7 AND s.token_version=1 AND s.revoked_at IS NULL AND s.refresh_expires_at>clock_timestamp() AND a.revoked_at IS NULL AND a.expires_at>clock_timestamp())`, a.session, g.ID, g.Company, a.actor, a.source, g.Epoch, a.binding.LocalSessionHash[:]).Scan(&valid)
	if e != nil || !valid {
		return g, ci.ErrDenied
	}
	return g, nil
}
func validRedemption(in ci.Redemption) bool {
	return ci.ValidID(in.AttemptID) && ci.ValidID(in.OperationID) && ci.ValidSecret(in.InstallationProof) && ci.ValidSecret(in.Proof) && ci.ValidSecret(in.LocalSession) && in.ConnectionRevision >= 0
}
func matchesRedemption(a ci.Attempt, in ci.Redemption) bool {
	return a.ID == in.AttemptID && a.InstallationHash == ci.InstallationDigest(in.InstallationProof) && a.ProofHash == ci.Digest(in.Proof) && a.LocalSessionHash == ci.Digest(in.LocalSession) && a.LocalOrigin == in.LocalOrigin && a.ConnectionRevision == in.ConnectionRevision
}
func (r *CompanyInterfaceLifecycle) Redeem(ctx context.Context, in ci.Redemption) (ci.Tokens, error) {
	if !validRedemption(in) {
		return ci.Tokens{}, ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return ci.Tokens{}, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return ci.Tokens{}, e
	}
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE id=$1 FOR UPDATE`, in.AttemptID))
	if e != nil || !matchesRedemption(a.binding, in) {
		return ci.Tokens{}, ci.ErrDenied
	}
	if a.state == "pending" && time.Now().Before(a.expires) {
		return ci.Tokens{}, ci.ErrPending
	}
	if a.state != "approved" || !time.Now().Before(a.recovery) {
		return ci.Tokens{}, ci.ErrDenied
	}
	if a.redeemOp != "" && a.redeemOp != in.OperationID {
		return ci.Tokens{}, ci.ErrConflict
	}
	var company string
	if e = tx.QueryRowContext(ctx, `SELECT organization_id::text FROM company_interface_grants WHERE id=$1`, a.grant).Scan(&company); e != nil {
		return ci.Tokens{}, ci.ErrDenied
	}
	if e = ciOrgLock(ctx, tx, company); e != nil {
		return ci.Tokens{}, e
	}
	if _, e = ciLockActor(ctx, tx, identity.User{ID: a.actor, SourceSessionID: a.source}); e != nil {
		return ci.Tokens{}, e
	}
	grant, e := r.liveIssued(ctx, tx, a)
	if e != nil {
		return ci.Tokens{}, e
	}
	var result ci.Tokens
	if e = r.cipher.Open(a.receipt, ciReceiptBinding(a), &result); e != nil {
		return result, e
	}
	if result.AttemptID != a.binding.ID || result.GrantID != grant.ID || result.Company != grant.Company || result.Actor != a.actor || result.SessionID != a.session || result.GrantEpoch != grant.Epoch || result.Version != 1 || result.LocalSessionHash != a.binding.LocalSessionHash || result.InstallationHash != a.binding.InstallationHash || result.ConnectionRevision != a.binding.ConnectionRevision || result.CentralOrigin != r.central || result.LocalOrigin != a.binding.LocalOrigin {
		return ci.Tokens{}, ci.ErrDenied
	}
	if a.redeemOp == "" {
		if _, e = tx.ExecContext(ctx, `UPDATE company_interface_attempts SET redeem_operation=$2 WHERE id=$1`, a.binding.ID, in.OperationID); e != nil {
			return ci.Tokens{}, e
		}
		if e = r.checkpoint("redeem_operation"); e != nil {
			return ci.Tokens{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return ci.Tokens{}, e
	}
	return result, nil
}

// Cancel is proof-bound and never revokes a committed issuance or a newer
// attempt. A lost approval response remains recoverable, not silently deleted.
func (r *CompanyInterfaceLifecycle) Cancel(ctx context.Context, in ci.Redemption) error {
	if !validRedemption(in) {
		return ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return e
	}
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE id=$1 FOR UPDATE`, in.AttemptID))
	if e != nil || !matchesRedemption(a.binding, in) {
		return ci.ErrDenied
	}
	if a.state == "approved" {
		return ci.ErrConflict
	}
	if a.state == "cancelled" {
		return tx.Commit()
	}
	if a.state != "pending" {
		return ci.ErrDenied
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_attempts SET state='cancelled' WHERE id=$1`, in.AttemptID); e != nil {
		return e
	}
	return tx.Commit()
}

type ciFamily struct {
	id, grant, company, actor, source string
	local, refresh                    []byte
	grantEpoch, epoch, version        int64
	expires                           time.Time
	revoked                           bool
}

func readCIFamily(ctx context.Context, tx *sql.Tx, id string, lock bool) (f ciFamily, e error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	e = tx.QueryRowContext(ctx, `SELECT id::text,grant_id::text,organization_id::text,actor_id::text,COALESCE(source_session_id::text,''),local_session_hash,refresh_hash,grant_epoch,session_epoch,token_version,refresh_expires_at,revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`+suffix, id).Scan(&f.id, &f.grant, &f.company, &f.actor, &f.source, &f.local, &f.refresh, &f.grantEpoch, &f.epoch, &f.version, &f.expires, &f.revoked)
	if e != nil || !ci.ValidID(f.source) || len(f.local) != 32 || len(f.refresh) != 32 {
		return f, ci.ErrDenied
	}
	return f, nil
}
func rotationBinding(f ciFamily, operation string, fp [32]byte) any {
	return []any{ci.Purpose, ci.Version, "rotation", f.id, f.grant, f.company, f.actor, f.source, f.epoch, operation, fp}
}
func (r *CompanyInterfaceLifecycle) Refresh(ctx context.Context, in ci.Refresh) (ci.Tokens, error) {
	if !ci.ValidID(in.OperationID) || !ci.ValidID(in.SessionID) || !ci.ValidSecret(in.GrantCredential) || !ci.ValidSecret(in.RefreshToken) || !ci.ValidSecret(in.LocalSession) || in.Version < 1 {
		return ci.Tokens{}, ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return ci.Tokens{}, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return ci.Tokens{}, e
	}
	f, e := readCIFamily(ctx, tx, in.SessionID, false)
	if e != nil {
		return ci.Tokens{}, e
	}
	if e = ciOrgLock(ctx, tx, f.company); e != nil {
		return ci.Tokens{}, e
	}
	sourceExpiry, e := ciLockActor(ctx, tx, identity.User{ID: f.actor, SourceSessionID: f.source})
	if e != nil {
		return ci.Tokens{}, e
	}
	if _, e = ciRole(ctx, tx, f.company, f.actor); e != nil {
		return ci.Tokens{}, e
	}
	grant, e := ciReadGrant(ctx, tx, f.grant)
	if e != nil || grant.Validate(time.Now()) != nil || grant.CentralOrigin != r.central || grant.LocalOrigin != in.LocalOrigin {
		return ci.Tokens{}, ci.ErrDenied
	}
	var credential []byte
	if e = tx.QueryRowContext(ctx, `SELECT credential_hash FROM company_interface_grants WHERE id=$1`, grant.ID).Scan(&credential); e != nil {
		return ci.Tokens{}, ci.ErrDenied
	}
	gh := ci.Digest(in.GrantCredential)
	if !bytes.Equal(credential, gh[:]) {
		return ci.Tokens{}, ci.ErrDenied
	}
	f, e = readCIFamily(ctx, tx, in.SessionID, true)
	if e != nil {
		return ci.Tokens{}, e
	}
	lh := ci.Digest(in.LocalSession)
	if f.revoked || !time.Now().Before(f.expires) || f.grantEpoch != grant.Epoch || !bytes.Equal(f.local, lh[:]) {
		return ci.Tokens{}, ci.ErrDenied
	}
	fp := ci.Fingerprint(in)
	var previousHash, receipt []byte
	var toVersion int64
	var recovery time.Time
	e = tx.QueryRowContext(ctx, `SELECT request_hash,to_version,receipt,recovery_expires_at FROM company_interface_rotations WHERE session_id=$1 AND operation_id=$2`, f.id, in.OperationID).Scan(&previousHash, &toVersion, &receipt, &recovery)
	if e == nil {
		if !bytes.Equal(previousHash, fp[:]) || f.version != toVersion || !time.Now().Before(recovery) {
			return ci.Tokens{}, ci.ErrConflict
		}
		var result ci.Tokens
		if e = r.cipher.Open(receipt, rotationBinding(f, in.OperationID, fp), &result); e != nil {
			return ci.Tokens{}, e
		}
		if result.SessionID != f.id || result.Version != f.version || result.SessionEpoch != f.epoch {
			return ci.Tokens{}, ci.ErrDenied
		}
		return result, tx.Commit()
	}
	if e != sql.ErrNoRows {
		return ci.Tokens{}, e
	}
	oldHash := ci.Digest(in.RefreshToken)
	if f.version != in.Version || !bytes.Equal(f.refresh, oldHash[:]) {
		return ci.Tokens{}, ci.ErrConflict
	}
	access, e := ci.NewSecret()
	if e != nil {
		return ci.Tokens{}, e
	}
	refresh, e := ci.NewSecret()
	if e != nil {
		return ci.Tokens{}, e
	}
	now := time.Now().UTC()
	refreshExpiry := now.Add(ci.RefreshLifetime)
	if sourceExpiry.Before(refreshExpiry) {
		refreshExpiry = sourceExpiry
	}
	accessExpiry := now.Add(ci.AccessLifetime)
	if refreshExpiry.Before(accessExpiry) {
		accessExpiry = refreshExpiry
	}
	result := ci.Tokens{GrantID: grant.ID, Company: f.company, Actor: f.actor, SessionID: f.id, LocalOrigin: grant.LocalOrigin, CentralOrigin: grant.CentralOrigin, InstallationHash: grant.InstallationHash, LocalSessionHash: lh, GrantEpoch: grant.Epoch, SessionEpoch: f.epoch, Version: f.version + 1, AccessToken: access, RefreshToken: refresh, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry}
	ah, rh := ci.Digest(access), ci.Digest(refresh)
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_sessions SET token_version=token_version+1,access_hash=$2,access_expires_at=$3,refresh_hash=$4,refresh_expires_at=$5 WHERE id=$1`, f.id, ah[:], accessExpiry, rh[:], refreshExpiry); e != nil {
		return ci.Tokens{}, e
	}
	if e = r.checkpoint("rotation_family"); e != nil {
		return ci.Tokens{}, e
	}
	receipt, e = r.cipher.Seal(result, rotationBinding(f, in.OperationID, fp))
	if e != nil {
		return ci.Tokens{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_rotations(session_id,operation_id,request_hash,from_version,to_version,receipt,recovery_expires_at) VALUES($1,$2,$3,$4,$5,$6,LEAST($7,clock_timestamp()+INTERVAL '7 days'))`, f.id, in.OperationID, fp[:], f.version, result.Version, receipt, refreshExpiry); e != nil {
		return ci.Tokens{}, e
	}
	if e = r.checkpoint("rotation_receipt"); e != nil {
		return ci.Tokens{}, e
	}
	if e = tx.Commit(); e != nil {
		return ci.Tokens{}, e
	}
	return result, nil
}

// Revoke requires a current central Supermanager of this company. Global/local
// administrator flags do not substitute for that membership.
func (r *CompanyInterfaceLifecycle) Revoke(ctx context.Context, grantID string) error {
	u, e := ciActor(ctx)
	if e != nil || !ci.ValidID(grantID) {
		return ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return e
	}
	var company string
	if e = tx.QueryRowContext(ctx, `SELECT organization_id::text FROM company_interface_grants WHERE id=$1`, grantID).Scan(&company); e != nil {
		return ci.ErrDenied
	}
	if e = ciOrgLock(ctx, tx, company); e != nil {
		return e
	}
	if _, e = ciLockActor(ctx, tx, u); e != nil {
		return e
	}
	role, e := ciRole(ctx, tx, company, u.ID)
	if e != nil || role != "supermanager" {
		return ci.ErrDenied
	}
	g, e := ciReadGrant(ctx, tx, grantID)
	if e != nil || g.CentralOrigin != r.central {
		return ci.ErrDenied
	}
	if e = ciRevokeGrant(ctx, tx, grantID); e != nil {
		return e
	}
	return tx.Commit()
}

// Logout revokes only the authenticated delegated family. It never deletes the
// central browser login, installation identity, another family or an SDK key.
func (r *CompanyInterfaceLifecycle) Logout(ctx context.Context, c ci.Credentials) error {
	authority := NewCompanyInterfaceAuthority(r.db)
	gate := ci.Gate{Authority: authority, Keys: authority, Recipients: authority, CentralOrigin: r.central}
	p, e := gate.AuthenticateBrowser(ctx, c, time.Now())
	if e != nil {
		return e
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE id=$1 AND grant_id=$2 AND actor_id=$3 AND revoked_at IS NULL`, p.SessionID, p.Grant.ID, p.BrowserActor)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// Reconcile reads the exact original operation even after its actor/session is
// revoked. Possession of the installation/device proofs authorizes only this
// non-user reconciliation, never renewed browser or SDK authority.
func (r *CompanyInterfaceLifecycle) Reconcile(ctx context.Context, in ci.Redemption) (ci.Reconciliation, error) {
	result, _, e := r.reconcile(ctx, in, false)
	return result, e
}

// RecoverInstallation releases only the permanent installation credential to
// the original proof holder. It never recovers actor/session/Playground secrets.
func (r *CompanyInterfaceLifecycle) RecoverInstallation(ctx context.Context, in ci.Redemption) (ci.LocalInstallation, error) {
	_, result, e := r.reconcile(ctx, in, true)
	return result, e
}
func (r *CompanyInterfaceLifecycle) reconcile(ctx context.Context, in ci.Redemption, recoverSecret bool) (ci.Reconciliation, ci.LocalInstallation, error) {
	var empty ci.Reconciliation
	var installation ci.LocalInstallation
	if !validRedemption(in) {
		return empty, installation, ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return empty, installation, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return empty, installation, e
	}
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE id=$1 FOR UPDATE`, in.AttemptID))
	if e != nil || !matchesRedemption(a.binding, in) {
		return empty, installation, ci.ErrDenied
	}
	result := ci.Reconciliation{Binding: a.binding, Fingerprint: ci.Fingerprint(a.binding), State: a.state}
	if a.state != "approved" {
		if recoverSecret {
			return empty, installation, ci.ErrDenied
		}
		return result, installation, tx.Commit()
	}
	// Recovery retention is finite and separate from the short challenge lifetime.
	if !time.Now().Before(a.recovery) {
		return empty, installation, ci.ErrDenied
	}
	g, e := ciReadGrant(ctx, tx, a.grant)
	if e != nil || g.InstallationHash != a.binding.InstallationHash || g.CentralOrigin != a.binding.CentralOrigin || g.LocalOrigin != a.binding.LocalOrigin {
		return empty, installation, ci.ErrDenied
	}
	var fgrant string
	var issuedEpoch int64
	e = tx.QueryRowContext(ctx, `SELECT grant_id::text,grant_epoch,revoked_at IS NOT NULL FROM company_interface_sessions WHERE id=$1`, a.session).Scan(&fgrant, &issuedEpoch, &result.SessionRevoked)
	if e != nil || fgrant != g.ID {
		return empty, installation, ci.ErrDenied
	}
	result.GrantID = g.ID
	result.Company = g.Company
	result.SessionID = a.session
	result.GrantEpoch = issuedEpoch
	result.GrantRevoked = g.Revoked
	// A terminal grant may have advanced its revocation epoch; the reconciliation
	// names the exact issued epoch, never an unrelated successor grant.
	if !g.Revoked && g.Epoch != issuedEpoch {
		return empty, installation, ci.ErrConflict
	}
	if recoverSecret {
		if g.Validate(time.Now()) != nil || a.binding.Mode == "login" {
			return empty, installation, ci.ErrDenied
		}
		var tokens ci.Tokens
		if e = r.cipher.Open(a.receipt, ciReceiptBinding(a), &tokens); e != nil {
			return empty, installation, e
		}
		if tokens.GrantID != g.ID || tokens.GrantEpoch != issuedEpoch || tokens.Company != g.Company || tokens.InstallationHash != g.InstallationHash || tokens.CentralOrigin != g.CentralOrigin || tokens.LocalOrigin != g.LocalOrigin || !ci.ValidSecret(tokens.InstallationCredential) {
			return empty, installation, ci.ErrDenied
		}
		installation = ci.LocalInstallation{GrantID: g.ID, GrantEpoch: issuedEpoch, Company: g.Company, InstallationHash: g.InstallationHash, CentralOrigin: g.CentralOrigin, LocalOrigin: g.LocalOrigin, Credential: tokens.InstallationCredential}
	}
	return result, installation, tx.Commit()
}

// RevokeIssuedFamily is terminal cleanup only. The exact original pending proof
// authorizes revocation of its own issued family even after access expiry or
// membership removal; it never renews user authority or revokes the installation.
func (r *CompanyInterfaceLifecycle) RevokeIssuedFamily(ctx context.Context, in ci.Redemption) error {
	if !validRedemption(in) {
		return ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return e
	}
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE id=$1 FOR UPDATE`, in.AttemptID))
	if e != nil || !matchesRedemption(a.binding, in) || a.binding.CentralOrigin != r.central || a.state != "approved" || !time.Now().Before(a.recovery) {
		return ci.ErrDenied
	}
	g, e := ciReadGrant(ctx, tx, a.grant)
	if e != nil || g.InstallationHash != a.binding.InstallationHash || g.CentralOrigin != a.binding.CentralOrigin || g.LocalOrigin != a.binding.LocalOrigin {
		return ci.ErrDenied
	}
	f, e := readCIFamily(ctx, tx, a.session, true)
	if e != nil || f.grant != g.ID || f.company != g.Company || f.actor != a.actor || f.source != a.source || !bytes.Equal(f.local, a.binding.LocalSessionHash[:]) || f.grantEpoch < 1 || (!g.Revoked && f.grantEpoch != g.Epoch) {
		return ci.ErrDenied
	}
	if f.revoked {
		return tx.Commit()
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE id=$1 AND grant_id=$2 AND revoked_at IS NULL`, f.id, g.ID); e != nil {
		return e
	}
	if e = r.checkpoint("cleanup_family"); e != nil {
		return e
	}
	return tx.Commit()
}
