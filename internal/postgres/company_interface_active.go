package postgres

import (
	"bytes"
	"context"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
)

// RevokeFamilyProof is explicit terminal logout, never refresh-token replay
// handling. A stale ordinary Refresh does not call this path or revoke a family.
// Old proof is accepted only for its exact committed rotation within the fixed
// recovery deadline; a request hash or family identifier alone is no authority.
func (r *CompanyInterfaceLifecycle) RevokeFamilyProof(ctx context.Context, in ci.Refresh) (ci.FamilyRevocation, error) {
	var result ci.FamilyRevocation
	if !ci.ValidID(in.OperationID) || !ci.ValidID(in.SessionID) || !ci.ValidSecret(in.GrantCredential) || !ci.ValidSecret(in.RefreshToken) || !ci.ValidSecret(in.LocalSession) || in.Version < 1 {
		return result, ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return result, e
	}
	f, e := readCIFamily(ctx, tx, in.SessionID, false)
	if e != nil {
		return result, e
	}
	g, e := ciReadGrant(ctx, tx, f.grant)
	if e != nil || g.Company != f.company || g.CentralOrigin != r.central || g.LocalOrigin != in.LocalOrigin {
		return result, ci.ErrDenied
	}
	f, e = readCIFamily(ctx, tx, in.SessionID, true)
	if e != nil || f.grant != g.ID || f.company != g.Company {
		return result, ci.ErrDenied
	}
	var credential []byte
	if e = tx.QueryRowContext(ctx, `SELECT credential_hash FROM company_interface_grants WHERE id=$1`, g.ID).Scan(&credential); e != nil {
		return result, e
	}
	gh, lh, rh, fp := ci.Digest(in.GrantCredential), ci.Digest(in.LocalSession), ci.Digest(in.RefreshToken), ci.Fingerprint(in)
	if !bytes.Equal(credential, gh[:]) || !bytes.Equal(f.local, lh[:]) || (!g.Revoked && g.Epoch != f.grantEpoch) {
		return result, ci.ErrDenied
	}
	current := in.Version == f.version && bytes.Equal(f.refresh, rh[:]) && time.Now().Before(f.expires.Add(ci.RecoveryLifetime))
	if !current {
		var hash []byte
		var toVersion int64
		var until time.Time
		e = tx.QueryRowContext(ctx, `SELECT request_hash,to_version,recovery_expires_at FROM company_interface_rotations WHERE session_id=$1 AND operation_id=$2 AND from_version=$3`, f.id, in.OperationID, in.Version).Scan(&hash, &toVersion, &until)
		if e != nil || !bytes.Equal(hash, fp[:]) || toVersion > f.version || !time.Now().Before(until) {
			return result, ci.ErrDenied
		}
	}
	if !f.revoked {
		if _, e = tx.ExecContext(ctx, `UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE id=$1 AND revoked_at IS NULL`, f.id); e != nil {
			return result, e
		}
		if e = r.checkpoint("active_logout_family"); e != nil {
			return result, e
		}
	}
	result = ci.FamilyRevocation{GrantID: g.ID, GrantEpoch: f.grantEpoch, SessionID: f.id, Version: in.Version, LocalOrigin: in.LocalOrigin, LocalSessionHash: lh, RequestHash: fp, Revoked: true}
	return result, tx.Commit()
}
