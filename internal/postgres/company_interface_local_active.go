package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/google/uuid"
)

type ciRowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *CompanyInterfaceLifecycle) readLocalActive(ctx context.Context, q ciRowQuery, local string, current, lock bool) (ci.ActiveContext, error) {
	var v ci.ActiveContext
	if !ci.ValidSecret(local) {
		return v, ci.ErrDenied
	}
	hash := ci.Digest(local)
	var actor, session, grant string
	var version, grantEpoch int64
	var encrypted []byte
	query := `SELECT c.context_epoch,c.connection_revision,c.actor_id::text,c.session_id::text,c.token_version,c.grant_id::text,c.grant_epoch,c.ciphertext FROM company_interface_local_contexts c`
	if current {
		query += ` JOIN company_interface_local_control g ON g.singleton=TRUE AND g.revision=c.connection_revision AND g.grant_id=c.grant_id AND g.grant_epoch=c.grant_epoch`
	}
	query += ` WHERE c.local_session_hash=$1 AND c.ciphertext IS NOT NULL AND c.pending_attempt IS NULL`
	if lock {
		query += ` FOR UPDATE OF c`
	}
	if e := q.QueryRowContext(ctx, query, hash[:]).Scan(&v.Epoch, &v.Revision, &actor, &session, &version, &grant, &grantEpoch, &encrypted); e != nil {
		return v, ci.ErrDenied
	}
	if e := r.cipher.Open(encrypted, localAAD("session", hash, v.Epoch, v.Revision, session, version), &v.Tokens); e != nil {
		return v, e
	}
	t := &v.Tokens
	if t.Actor != actor || t.SessionID != session || t.Version != version || t.GrantID != grant || t.GrantEpoch != grantEpoch || t.LocalSessionHash != hash || t.ConnectionRevision != v.Revision || t.CentralOrigin != r.central {
		return ci.ActiveContext{}, ci.ErrDenied
	}
	if t.InstallationCredential == "" {
		stored, cipher, e := readLocalInstallation(ctx, q)
		if e != nil || stored.GrantID != t.GrantID || stored.GrantEpoch != t.GrantEpoch || stored.Company != t.Company {
			return ci.ActiveContext{}, ci.ErrConflict
		}
		var installation ci.LocalInstallation
		if e = r.cipher.Open(cipher, localInstallationAAD(stored), &installation); e != nil {
			return ci.ActiveContext{}, e
		}
		check := installation
		check.Credential = ""
		if check != stored || !ci.ValidSecret(installation.Credential) {
			return ci.ActiveContext{}, ci.ErrDenied
		}
		t.InstallationCredential = installation.Credential
	}
	if !ci.ValidSecret(t.InstallationCredential) || !ci.ValidSecret(t.RefreshToken) || !ci.ValidSecret(t.AccessToken) {
		return ci.ActiveContext{}, ci.ErrDenied
	}
	v.LocalSession = local
	return v, nil
}
func (r *CompanyInterfaceLifecycle) LocalActive(ctx context.Context, local string) (ci.ActiveContext, error) {
	v, e := r.readLocalActive(ctx, r.db, local, true, false)
	if e != nil {
		return v, e
	}
	return v, r.CheckLocalEpoch(ctx, local, v.Epoch, v.Revision, v.Tokens.Version, v.Tokens.SessionID, v.Tokens.Actor)
}
func localOperationAAD(v ci.LocalOperation, local, request [32]byte) any {
	return []any{ci.Purpose, ci.Version, "local-operation", v.ID, v.Kind, local, v.Epoch, v.Revision, v.GrantID, v.GrantEpoch, v.Actor, v.Request.SessionID, v.SessionEpoch, v.Request.Version, v.CentralOrigin, request}
}

const ciLocalOperationColumns = `id::text,kind,local_session_hash,context_epoch,connection_revision,grant_id::text,grant_epoch,actor_id::text,session_id::text,session_epoch,token_version,central_origin,request_hash,state,ciphertext`

func (r *CompanyInterfaceLifecycle) scanLocalOperation(row interface{ Scan(...any) error }) (ci.LocalOperation, error) {
	var binding ci.LocalOperation
	var local, request, encrypted []byte
	e := row.Scan(&binding.ID, &binding.Kind, &local, &binding.Epoch, &binding.Revision, &binding.GrantID, &binding.GrantEpoch, &binding.Actor, &binding.Request.SessionID, &binding.SessionEpoch, &binding.Request.Version, &binding.CentralOrigin, &request, &binding.State, &encrypted)
	if e != nil {
		return binding, e
	}
	if len(local) != 32 || len(request) != 32 || binding.State != "pending" {
		return binding, ci.ErrConflict
	}
	var lh, fp [32]byte
	copy(lh[:], local)
	copy(fp[:], request)
	var v ci.LocalOperation
	if e = r.cipher.Open(encrypted, localOperationAAD(binding, lh, fp), &v); e != nil {
		return v, e
	}
	if ci.Digest(v.Request.LocalSession) != lh || ci.Fingerprint(v.Request) != fp {
		return v, ci.ErrDenied
	}
	compare := v
	compare.Request = ci.Refresh{SessionID: v.Request.SessionID, Version: v.Request.Version}
	if compare != binding {
		return v, ci.ErrDenied
	}
	return v, nil
}
func (r *CompanyInterfaceLifecycle) saveLocalOperation(ctx context.Context, tx *sql.Tx, v ci.LocalOperation) error {
	lh, fp := ci.Digest(v.Request.LocalSession), ci.Fingerprint(v.Request)
	ciphertext, e := r.cipher.Seal(v, localOperationAAD(v, lh, fp))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO company_interface_local_operations(id,kind,local_session_hash,context_epoch,connection_revision,grant_id,grant_epoch,actor_id,session_id,session_epoch,token_version,central_origin,request_hash,state,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'pending',$14)`, v.ID, v.Kind, lh[:], v.Epoch, v.Revision, v.GrantID, v.GrantEpoch, v.Actor, v.Request.SessionID, v.SessionEpoch, v.Request.Version, v.CentralOrigin, fp[:], ciphertext)
	return e
}
func localOperation(kind string, v ci.ActiveContext) ci.LocalOperation {
	id := uuid.NewString()
	return ci.LocalOperation{ID: id, Kind: kind, State: "pending", CentralOrigin: v.Tokens.CentralOrigin, GrantID: v.Tokens.GrantID, GrantEpoch: v.Tokens.GrantEpoch, Actor: v.Tokens.Actor, Epoch: v.Epoch, Revision: v.Revision, SessionEpoch: v.Tokens.SessionEpoch, Request: ci.Refresh{OperationID: id, GrantCredential: v.Tokens.InstallationCredential, RefreshToken: v.Tokens.RefreshToken, LocalSession: v.LocalSession, LocalOrigin: v.Tokens.LocalOrigin, SessionID: v.Tokens.SessionID, Version: v.Tokens.Version}}
}
func (r *CompanyInterfaceLifecycle) BeginLocalRotation(ctx context.Context, expected ci.ActiveContext) (ci.LocalOperation, error) {
	var empty ci.LocalOperation
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	if e = localRevision(ctx, tx, expected.Revision); e != nil {
		return empty, e
	}
	actual, e := r.readLocalActive(ctx, tx, expected.LocalSession, true, true)
	if e != nil {
		return empty, e
	}
	if actual.Epoch != expected.Epoch || actual.Revision != expected.Revision || ci.Fingerprint(actual.Tokens) != ci.Fingerprint(expected.Tokens) || !time.Now().Before(actual.Tokens.RefreshExpiresAt) {
		return empty, ci.ErrConflict
	}
	lh := ci.Digest(actual.LocalSession)
	prior, e := r.scanLocalOperation(tx.QueryRowContext(ctx, `SELECT `+ciLocalOperationColumns+` FROM company_interface_local_operations WHERE local_session_hash=$1 AND context_epoch=$2 AND session_id=$3 AND token_version=$4 AND kind='rotation'`, lh[:], actual.Epoch, actual.Tokens.SessionID, actual.Tokens.Version))
	if e == nil {
		return prior, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return empty, e
	}
	op := localOperation("rotation", actual)
	if e = r.saveLocalOperation(ctx, tx, op); e != nil {
		return empty, e
	}
	return op, tx.Commit()
}
func (r *CompanyInterfaceLifecycle) CommitLocalRotation(ctx context.Context, op ci.LocalOperation, result ci.Tokens) error {
	if op.Kind != "rotation" || op.CentralOrigin != r.central {
		return ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = localRevision(ctx, tx, op.Revision); e != nil {
		return e
	}
	actual, e := r.readLocalActive(ctx, tx, op.Request.LocalSession, true, true)
	if e != nil {
		return ci.ErrConflict
	}
	if actual.Epoch != op.Epoch || actual.Revision != op.Revision || actual.Tokens.Actor != op.Actor || actual.Tokens.SessionID != op.Request.SessionID || actual.Tokens.Version != op.Request.Version {
		return ci.ErrConflict
	}
	stored, e := r.scanLocalOperation(tx.QueryRowContext(ctx, `SELECT `+ciLocalOperationColumns+` FROM company_interface_local_operations WHERE id=$1 FOR UPDATE`, op.ID))
	if e != nil {
		return e
	}
	if ci.Fingerprint(stored) != ci.Fingerprint(op) {
		return ci.ErrConflict
	}
	prior := actual.Tokens
	hash := ci.Digest(actual.LocalSession)
	if result.Version != prior.Version+1 || result.SessionID != prior.SessionID || result.Actor != prior.Actor || result.Company != prior.Company || result.GrantID != prior.GrantID || result.GrantEpoch != prior.GrantEpoch || result.SessionEpoch != prior.SessionEpoch || result.LocalSessionHash != hash || result.InstallationHash != prior.InstallationHash || result.CentralOrigin != prior.CentralOrigin || result.LocalOrigin != prior.LocalOrigin || !ci.ValidSecret(result.AccessToken) || !ci.ValidSecret(result.RefreshToken) {
		return ci.ErrDenied
	}
	result.ConnectionRevision = actual.Revision
	result.AttemptID = prior.AttemptID
	result.InstallationCredential = prior.InstallationCredential
	result.PlaygroundKey = prior.PlaygroundKey
	encrypted, e := r.cipher.Seal(result, localAAD("session", hash, actual.Epoch, actual.Revision, result.SessionID, result.Version))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET token_version=$2,ciphertext=$3 WHERE local_session_hash=$1`, hash[:], result.Version, encrypted); e != nil {
		return e
	}
	if e = r.checkpoint("local_rotation_adoption"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_operations SET state='settled',ciphertext=NULL,settled_at=clock_timestamp() WHERE id=$1`, op.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *CompanyInterfaceLifecycle) BeginLocalLogout(ctx context.Context, local string, epoch int64) (ci.LocalOperation, error) {
	var empty ci.LocalOperation
	if !ci.ValidSecret(local) || epoch < 1 {
		return empty, ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	hash := ci.Digest(local)
	prior, e := r.scanLocalOperation(tx.QueryRowContext(ctx, `SELECT `+ciLocalOperationColumns+` FROM company_interface_local_operations WHERE local_session_hash=$1 AND context_epoch=$2 AND kind='logout' AND state='pending' FOR UPDATE`, hash[:], epoch))
	if e == nil {
		return prior, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return empty, e
	}
	actual, e := r.readLocalActive(ctx, tx, local, false, true)
	if e != nil {
		return empty, e
	}
	if actual.Epoch != epoch {
		return empty, ci.ErrConflict
	}
	op := localOperation("logout", actual)
	// An uncertain committed rotation has already consumed the current token.
	// Preserve its exact original request so central terminal cleanup can match it.
	rotation, e := r.scanLocalOperation(tx.QueryRowContext(ctx, `SELECT `+ciLocalOperationColumns+` FROM company_interface_local_operations WHERE local_session_hash=$1 AND context_epoch=$2 AND session_id=$3 AND token_version=$4 AND kind='rotation' AND state='pending'`, hash[:], epoch, actual.Tokens.SessionID, actual.Tokens.Version))
	if e == nil {
		op.Request = rotation.Request
	} else if !errors.Is(e, sql.ErrNoRows) {
		return empty, e
	}
	if e = r.saveLocalOperation(ctx, tx, op); e != nil {
		return empty, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET context_epoch=context_epoch+1,actor_id=NULL,session_id=NULL,token_version=NULL,ciphertext=NULL,grant_id=NULL,grant_epoch=NULL WHERE local_session_hash=$1 AND context_epoch=$2`, hash[:], epoch); e != nil {
		return empty, e
	}
	if e = r.checkpoint("local_logout_invalidation"); e != nil {
		return empty, e
	}
	return op, tx.Commit()
}
func (r *CompanyInterfaceLifecycle) SettleLocalLogout(ctx context.Context, op ci.LocalOperation, result ci.FamilyRevocation) error {
	hash, fp := ci.Digest(op.Request.LocalSession), ci.Fingerprint(op.Request)
	if op.Kind != "logout" || !result.Revoked || result.GrantID != op.GrantID || result.GrantEpoch != op.GrantEpoch || result.SessionID != op.Request.SessionID || result.Version != op.Request.Version || result.LocalOrigin != op.Request.LocalOrigin || result.LocalSessionHash != hash || result.RequestHash != fp {
		return ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	stored, e := r.scanLocalOperation(tx.QueryRowContext(ctx, `SELECT `+ciLocalOperationColumns+` FROM company_interface_local_operations WHERE id=$1 FOR UPDATE`, op.ID))
	if e != nil {
		return e
	}
	if ci.Fingerprint(stored) != ci.Fingerprint(op) {
		return ci.ErrConflict
	}
	// No current-global or live-context rewrite: an old logout may finish after a
	// rebind/new sign-in and must never affect the successor context.
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_operations SET state='settled',ciphertext=NULL,settled_at=clock_timestamp() WHERE local_session_hash=$1 AND context_epoch=$2 AND session_id=$3 AND state='pending'`, hash[:], op.Epoch, op.Request.SessionID); e != nil {
		return e
	}
	return tx.Commit()
}

var _ ci.ActiveStore = (*CompanyInterfaceLifecycle)(nil)
