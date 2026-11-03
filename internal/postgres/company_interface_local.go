package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

// These server-only local control primitives do not enable a connection mode.
// The eventual transport must check this epoch before AND after every body.
// They store only pending authorization and encrypted delegated credentials.
func localAAD(kind string, hash [32]byte, epoch, revision int64, id string, version int64) any {
	return []any{ci.Purpose, ci.Version, kind, hash, epoch, revision, id, version}
}
func localRevision(ctx context.Context, tx *sql.Tx, expected int64) error {
	var current int64
	if e := tx.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE FOR SHARE`).Scan(&current); e != nil {
		return e
	}
	if current != expected {
		return ci.ErrConflict
	}
	return nil
}

// AdvanceLocalRevision is a CAS invalidation primitive, not mode activation.
// Pending proofs remain stored at their original revision for exact cleanup.
func (r *CompanyInterfaceLifecycle) AdvanceLocalRevision(ctx context.Context, expected int64) error {
	result, e := r.db.ExecContext(ctx, `UPDATE company_interface_local_control SET revision=revision+1 WHERE singleton=TRUE AND revision=$1`, expected)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ci.ErrConflict
	}
	return nil
}
func (r *CompanyInterfaceLifecycle) BeginLocal(ctx context.Context, in ci.Start) (int64, error) {
	origin, e := installation.NormalizeOrigin(in.LocalOrigin)
	if e != nil || origin != in.LocalOrigin || !ci.ValidID(in.AttemptID) || !ci.ValidSecret(in.LocalSession) || !ci.ValidSecret(in.InstallationProof) || !ci.ValidSecret(in.Proof) || ci.Negotiate(in.Protocol) != nil {
		return 0, ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if e = localRevision(ctx, tx, in.ConnectionRevision); e != nil {
		return 0, e
	}
	hash := ci.Digest(in.LocalSession)
	if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_local_contexts(local_session_hash,connection_revision) VALUES($1,$2) ON CONFLICT(local_session_hash) DO NOTHING`, hash[:], in.ConnectionRevision); e != nil {
		return 0, e
	}
	var epoch, revision int64
	var attempt string
	var pending []byte
	if e = tx.QueryRowContext(ctx, `SELECT context_epoch,connection_revision,COALESCE(pending_attempt::text,''),pending_ciphertext FROM company_interface_local_contexts WHERE local_session_hash=$1 FOR UPDATE`, hash[:]).Scan(&epoch, &revision, &attempt, &pending); e != nil {
		return 0, e
	}
	if attempt != "" {
		var prior ci.Start
		if e = r.cipher.Open(pending, localAAD("pending", hash, epoch, revision, attempt, 0), &prior); e != nil {
			return 0, e
		}
		if ci.Fingerprint(prior) != ci.Fingerprint(in) {
			return 0, ci.ErrConflict
		}
		return epoch, tx.Commit()
	}
	epoch++
	encrypted, e := r.cipher.Seal(in, localAAD("pending", hash, epoch, in.ConnectionRevision, in.AttemptID, 0))
	if e != nil {
		return 0, e
	}
	recovery := ci.LocalRecovery{Request: in, CentralOrigin: r.central, ContextEpoch: epoch}
	custody, e := r.cipher.Seal(recovery, localRecoveryAAD(hash, epoch, in.ConnectionRevision, in.AttemptID, r.central))
	if e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_local_recovery(id,local_session_hash,context_epoch,connection_revision,central_origin,state,ciphertext) VALUES($1,$2,$3,$4,$5,'pending',$6)`, in.AttemptID, hash[:], epoch, in.ConnectionRevision, r.central, custody); e != nil {
		return 0, ci.ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET context_epoch=$2,connection_revision=$3,pending_attempt=$4,pending_ciphertext=$5,actor_id=NULL,session_id=NULL,token_version=NULL,ciphertext=NULL WHERE local_session_hash=$1`, hash[:], epoch, in.ConnectionRevision, in.AttemptID, encrypted); e != nil {
		return 0, e
	}
	return epoch, tx.Commit()
}
func (r *CompanyInterfaceLifecycle) LocalPending(ctx context.Context, local string) (ci.Start, int64, error) {
	hash := ci.Digest(local)
	var epoch, revision int64
	var attempt string
	var encrypted []byte
	e := r.db.QueryRowContext(ctx, `SELECT context_epoch,connection_revision,pending_attempt::text,pending_ciphertext FROM company_interface_local_contexts WHERE local_session_hash=$1 AND pending_attempt IS NOT NULL`, hash[:]).Scan(&epoch, &revision, &attempt, &encrypted)
	if e != nil {
		var id string
		if err := r.db.QueryRowContext(ctx, `SELECT id::text FROM company_interface_local_recovery WHERE local_session_hash=$1 AND state IN ('pending','quarantined') ORDER BY context_epoch DESC LIMIT 1`, hash[:]).Scan(&id); err != nil {
			return ci.Start{}, 0, ci.ErrDenied
		}
		recovery, err := r.RecoverLocalAttempt(ctx, id)
		return recovery.Request, recovery.ContextEpoch, err
	}
	var result ci.Start
	if e = r.cipher.Open(encrypted, localAAD("pending", hash, epoch, revision, attempt, 0), &result); e != nil {
		return result, 0, e
	}
	if result.LocalSession != local || result.AttemptID != attempt || result.ConnectionRevision != revision {
		return ci.Start{}, 0, ci.ErrDenied
	}
	return result, epoch, nil
}
func (r *CompanyInterfaceLifecycle) ActivateLocal(ctx context.Context, in ci.Start, epoch int64, result ci.Tokens) error {
	hash := ci.Digest(in.LocalSession)
	if result.AttemptID != in.AttemptID || result.LocalSessionHash != hash || result.InstallationHash != ci.InstallationDigest(in.InstallationProof) || result.ConnectionRevision != in.ConnectionRevision || result.LocalOrigin != in.LocalOrigin || result.CentralOrigin != r.central || !ci.ValidID(result.Actor) || !ci.ValidID(result.Company) || !ci.ValidID(result.GrantID) || !ci.ValidID(result.SessionID) || result.Version != 1 || !ci.ValidSecret(result.AccessToken) || !ci.ValidSecret(result.RefreshToken) {
		return ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()

	// Serialize installation storage with connection revision changes and other
	// actors' activation. The grant remains separate from this user's context.
	var lockedRevision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE FOR UPDATE`).Scan(&lockedRevision); e != nil {
		return e
	}
	if lockedRevision != in.ConnectionRevision {
		return ci.ErrConflict
	}
	var pending []byte
	e = tx.QueryRowContext(ctx, `SELECT pending_ciphertext FROM company_interface_local_contexts WHERE local_session_hash=$1 AND context_epoch=$2 AND connection_revision=$3 AND pending_attempt=$4 FOR UPDATE`, hash[:], epoch, in.ConnectionRevision, in.AttemptID).Scan(&pending)
	if e != nil {
		return ci.ErrConflict
	}
	var prior ci.Start
	if e = r.cipher.Open(pending, localAAD("pending", hash, epoch, in.ConnectionRevision, in.AttemptID, 0), &prior); e != nil {
		return e
	}
	if ci.Fingerprint(prior) != ci.Fingerprint(in) {
		return ci.ErrConflict
	}
	if e = r.saveLocalInstallation(ctx, tx, in, result); e != nil {
		return e
	}
	encrypted, e := r.cipher.Seal(result, localAAD("session", hash, epoch, in.ConnectionRevision, result.SessionID, result.Version))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET pending_attempt=NULL,pending_ciphertext=NULL,actor_id=$2,session_id=$3,token_version=$4,ciphertext=$5,grant_id=$6,grant_epoch=$7 WHERE local_session_hash=$1`, hash[:], result.Actor, result.SessionID, result.Version, encrypted, result.GrantID, result.GrantEpoch)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_recovery SET state='settled',confirmed_outcome='activated',ciphertext=NULL WHERE id=$1 AND state='pending'`, in.AttemptID); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *CompanyInterfaceLifecycle) RotateLocal(ctx context.Context, local string, epoch, revision, version int64, result ci.Tokens) error {
	hash := ci.Digest(local)
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = localRevision(ctx, tx, revision); e != nil {
		return e
	}
	var encrypted []byte
	e = tx.QueryRowContext(ctx, `SELECT c.ciphertext FROM company_interface_local_contexts c JOIN company_interface_local_control g ON g.singleton=TRUE AND g.grant_id=c.grant_id AND g.grant_epoch=c.grant_epoch AND g.revision=c.connection_revision WHERE c.local_session_hash=$1 AND c.context_epoch=$2 AND c.connection_revision=$3 AND c.session_id=$4 AND c.token_version=$5 AND c.pending_attempt IS NULL FOR UPDATE OF c`, hash[:], epoch, revision, result.SessionID, version).Scan(&encrypted)
	if e != nil {
		return ci.ErrConflict
	}
	var prior ci.Tokens
	if e = r.cipher.Open(encrypted, localAAD("session", hash, epoch, revision, result.SessionID, version), &prior); e != nil {
		return e
	}
	if result.Version != version+1 || result.LocalSessionHash != hash || result.Actor != prior.Actor || result.Company != prior.Company || result.GrantID != prior.GrantID || result.GrantEpoch != prior.GrantEpoch || result.SessionEpoch != prior.SessionEpoch || result.InstallationHash != prior.InstallationHash || result.CentralOrigin != prior.CentralOrigin || result.LocalOrigin != prior.LocalOrigin || !ci.ValidSecret(result.AccessToken) || !ci.ValidSecret(result.RefreshToken) {
		return ci.ErrDenied
	}
	result.ConnectionRevision = revision
	result.AttemptID = prior.AttemptID
	result.InstallationCredential = prior.InstallationCredential
	result.PlaygroundKey = prior.PlaygroundKey
	encrypted, e = r.cipher.Seal(result, localAAD("session", hash, epoch, revision, result.SessionID, result.Version))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET token_version=$2,ciphertext=$3 WHERE local_session_hash=$1`, hash[:], result.Version, encrypted); e != nil {
		return e
	}
	return tx.Commit()
}

// ClearLocal invalidates this live browser context but quarantines uncertain
// proof custody. Logout is not evidence that a remote approval did not commit.
func (r *CompanyInterfaceLifecycle) ClearLocal(ctx context.Context, local string, epoch int64) error {
	hash := ci.Digest(local)
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var pending string
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(pending_attempt::text,'') FROM company_interface_local_contexts WHERE local_session_hash=$1 AND context_epoch=$2 FOR UPDATE`, hash[:], epoch).Scan(&pending); e != nil {
		return ci.ErrConflict
	}
	if pending != "" {
		if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_recovery SET state='quarantined' WHERE id=$1 AND state='pending'`, pending); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET context_epoch=context_epoch+1,pending_attempt=NULL,pending_ciphertext=NULL,actor_id=NULL,session_id=NULL,token_version=NULL,ciphertext=NULL,grant_id=NULL,grant_epoch=NULL WHERE local_session_hash=$1`, hash[:]); e != nil {
		return e
	}
	return tx.Commit()
}

// CheckLocalEpoch is the before/after body fence. Caller-supplied state never
// overrides stored global revision, actor, family or version.
func (r *CompanyInterfaceLifecycle) CheckLocalEpoch(ctx context.Context, local string, epoch, revision, version int64, session, actor string) error {
	hash := ci.Digest(local)
	var saved []byte
	e := r.db.QueryRowContext(ctx, `SELECT c.local_session_hash FROM company_interface_local_contexts c JOIN company_interface_local_control g ON g.singleton=TRUE AND g.revision=c.connection_revision AND g.grant_id=c.grant_id AND g.grant_epoch=c.grant_epoch WHERE c.local_session_hash=$1 AND c.context_epoch=$2 AND c.connection_revision=$3 AND c.token_version=$4 AND c.session_id=$5 AND c.actor_id=$6 AND c.pending_attempt IS NULL AND c.ciphertext IS NOT NULL`, hash[:], epoch, revision, version, session, actor).Scan(&saved)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !bytes.Equal(saved, hash[:]) {
		return ci.ErrConflict
	}
	return e
}

func localInstallationAAD(v ci.LocalInstallation) any {
	return []any{ci.Purpose, ci.Version, "local-installation", v.InstallationHash, v.GrantID, v.Company, v.CentralOrigin, v.LocalOrigin, v.GrantEpoch}
}
func readLocalInstallation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (ci.LocalInstallation, []byte, error) {
	var v ci.LocalInstallation
	var hash, encrypted []byte
	e := q.QueryRowContext(ctx, `SELECT installation_hash,grant_id::text,organization_id::text,central_origin,approved_local_origin,grant_epoch,grant_ciphertext FROM company_interface_local_control WHERE singleton=TRUE AND grant_id IS NOT NULL`).Scan(&hash, &v.GrantID, &v.Company, &v.CentralOrigin, &v.LocalOrigin, &v.GrantEpoch, &encrypted)
	if e != nil {
		return v, nil, e
	}
	if len(hash) != 32 {
		return v, nil, ci.ErrDenied
	}
	copy(v.InstallationHash[:], hash)
	return v, encrypted, nil
}

// LocalInstallation is for trusted server-side transport only. No HTTP handler
// exposes this credential or treats it as a user identity.
func (r *CompanyInterfaceLifecycle) LocalInstallation(ctx context.Context) (ci.LocalInstallation, error) {
	binding, encrypted, e := readLocalInstallation(ctx, r.db)
	if e != nil {
		return binding, ci.ErrDenied
	}
	var v ci.LocalInstallation
	if e = r.cipher.Open(encrypted, localInstallationAAD(binding), &v); e != nil {
		return ci.LocalInstallation{}, e
	}
	check := v
	check.Credential = ""
	if check != binding || !ci.ValidSecret(v.Credential) || v.CentralOrigin != r.central {
		return ci.LocalInstallation{}, ci.ErrDenied
	}
	return v, nil
}
func (r *CompanyInterfaceLifecycle) saveLocalInstallation(ctx context.Context, tx *sql.Tx, in ci.Start, result ci.Tokens) error {
	prior, _, e := readLocalInstallation(ctx, tx)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	exists := e == nil
	if exists {
		if prior.InstallationHash != result.InstallationHash || prior.Company != result.Company || prior.CentralOrigin != result.CentralOrigin {
			return ci.ErrDenied
		}
		if prior.GrantID == result.GrantID {
			if prior.GrantEpoch != result.GrantEpoch || prior.LocalOrigin != result.LocalOrigin {
				return ci.ErrDenied
			}
			return nil
		}
		if in.PreviousGrantID != prior.GrantID || in.Mode == "login" || result.GrantEpoch <= prior.GrantEpoch {
			return ci.ErrConflict
		}
	} else if in.Mode == "login" {
		return ci.ErrDenied
	}
	if !ci.ValidSecret(result.InstallationCredential) {
		return ci.ErrDenied
	}
	v := ci.LocalInstallation{GrantID: result.GrantID, Company: result.Company, CentralOrigin: result.CentralOrigin, LocalOrigin: result.LocalOrigin, Credential: result.InstallationCredential, InstallationHash: result.InstallationHash, GrantEpoch: result.GrantEpoch}
	encrypted, e := r.cipher.Seal(v, localInstallationAAD(v))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE company_interface_local_control SET installation_hash=$1,grant_id=$2,organization_id=$3,central_origin=$4,approved_local_origin=$5,grant_epoch=$6,grant_ciphertext=$7 WHERE singleton=TRUE`, v.InstallationHash[:], v.GrantID, v.Company, v.CentralOrigin, v.LocalOrigin, v.GrantEpoch, encrypted)
	return e
}

func localRecoveryAAD(hash [32]byte, epoch, revision int64, id, central string) any {
	return []any{localAAD("recovery", hash, epoch, revision, id, 0), central}
}
func readLocalRecovery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (ci.LocalRecovery, [32]byte, int64, []byte, error) {
	var v ci.LocalRecovery
	var hash [32]byte
	var stored, encrypted []byte
	var revision int64
	e := q.QueryRowContext(ctx, `SELECT local_session_hash,context_epoch,connection_revision,central_origin,state,ciphertext FROM company_interface_local_recovery WHERE id=$1 AND state IN ('pending','quarantined')`, id).Scan(&stored, &v.ContextEpoch, &revision, &v.CentralOrigin, &v.State, &encrypted)
	if e != nil || len(stored) != 32 {
		return v, hash, revision, nil, ci.ErrDenied
	}
	copy(hash[:], stored)
	return v, hash, revision, encrypted, nil
}

// RecoverLocalAttempt is only for server-side original-destination reconciliation.
// A quarantined record cannot pass ActivateLocal's live epoch/attempt CAS.
func (r *CompanyInterfaceLifecycle) RecoverLocalAttempt(ctx context.Context, id string) (ci.LocalRecovery, error) {
	binding, hash, revision, encrypted, e := readLocalRecovery(ctx, r.db, id)
	if e != nil {
		return binding, e
	}
	var v ci.LocalRecovery
	if e = r.cipher.Open(encrypted, localRecoveryAAD(hash, binding.ContextEpoch, revision, id, binding.CentralOrigin), &v); e != nil {
		return v, e
	}
	if v.Request.AttemptID != id || ci.Digest(v.Request.LocalSession) != hash || v.Request.ConnectionRevision != revision || v.CentralOrigin != binding.CentralOrigin || v.ContextEpoch != binding.ContextEpoch {
		return ci.LocalRecovery{}, ci.ErrDenied
	}
	v.State = binding.State
	return v, nil
}

// SettleLocalRecovery accepts only a positively confirmed terminal central
// outcome for this exact original attempt. Family logout alone cannot discard
// permanent-grant custody. The future HTTP adapter must obtain confirmation from
// recovery.CentralOrigin; this method is deliberately not a browser endpoint.
func (r *CompanyInterfaceLifecycle) SettleLocalRecovery(ctx context.Context, expected ci.LocalRecovery, confirmation ci.Reconciliation) error {
	if !validLocalConfirmation(expected, confirmation) {
		return ci.ErrDenied
	}
	outcome := ""
	switch confirmation.State {
	case "cancelled", "denied":
		if confirmation.GrantID != "" || confirmation.SessionID != "" {
			return ci.ErrDenied
		}
		outcome = confirmation.State
	case "approved":
		if !ci.ValidID(confirmation.GrantID) || !ci.ValidID(confirmation.SessionID) || confirmation.GrantEpoch < 1 || !confirmation.SessionRevoked {
			return ci.ErrConflict
		}
		outcome = "revoked"
	default:
		return ci.ErrPending
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var globalRevision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE FOR SHARE`).Scan(&globalRevision); e != nil {
		return e
	}
	if confirmation.State == "approved" && !confirmation.GrantRevoked {
		stored, encrypted, err := readLocalInstallation(ctx, tx)
		if err != nil {
			return ci.ErrConflict
		}
		var opened ci.LocalInstallation
		if err = r.cipher.Open(encrypted, localInstallationAAD(stored), &opened); err != nil {
			return err
		}
		if !matchesInstallationConfirmation(opened, confirmation) || !ci.ValidSecret(opened.Credential) {
			return ci.ErrConflict
		}
	}
	// Keep the same context-before-recovery lock order as logout/activation.
	_, originalHash, _, _, err := readLocalRecovery(ctx, tx, expected.Request.AttemptID)
	if err != nil {
		return err
	}
	var lockedHash []byte
	if err = tx.QueryRowContext(ctx, `SELECT local_session_hash FROM company_interface_local_contexts WHERE local_session_hash=$1 FOR UPDATE`, originalHash[:]).Scan(&lockedHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var id string
	if e = tx.QueryRowContext(ctx, `SELECT id::text FROM company_interface_local_recovery WHERE id=$1 FOR UPDATE`, expected.Request.AttemptID).Scan(&id); e != nil {
		return ci.ErrDenied
	}
	binding, hash, revision, encrypted, e := readLocalRecovery(ctx, tx, id)
	if e != nil {
		return e
	}
	var actual ci.LocalRecovery
	if e = r.cipher.Open(encrypted, localRecoveryAAD(hash, binding.ContextEpoch, revision, id, binding.CentralOrigin), &actual); e != nil {
		return e
	}
	expected.State = ""
	if ci.Fingerprint(expected) != ci.Fingerprint(actual) {
		return ci.ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_recovery SET state='settled',confirmed_outcome=$2,ciphertext=NULL WHERE id=$1`, id, outcome); e != nil {
		return e
	}
	// A positive cancel may arrive before logout. Clear only the matching
	// original pending context; a newer attempt/session is never affected.
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_local_contexts SET context_epoch=context_epoch+1,pending_attempt=NULL,pending_ciphertext=NULL,actor_id=NULL,session_id=NULL,token_version=NULL,ciphertext=NULL,grant_id=NULL,grant_epoch=NULL WHERE local_session_hash=$1 AND pending_attempt=$2 AND context_epoch=$3 AND connection_revision=$4`, hash[:], id, binding.ContextEpoch, revision); e != nil {
		return e
	}
	return tx.Commit()
}

func validLocalConfirmation(expected ci.LocalRecovery, c ci.Reconciliation) bool {
	binding := ci.BindStart(expected.CentralOrigin, expected.Request)
	return c.Binding == binding && c.Fingerprint == ci.Fingerprint(binding)
}
func matchesInstallationConfirmation(v ci.LocalInstallation, c ci.Reconciliation) bool {
	return v.GrantID == c.GrantID && v.GrantEpoch == c.GrantEpoch && v.Company == c.Company && v.InstallationHash == c.Binding.InstallationHash && v.CentralOrigin == c.Binding.CentralOrigin && v.LocalOrigin == c.Binding.LocalOrigin
}

// PreserveRecoveredInstallation stores installation-only custody under the
// original revision/previous-grant CAS. It never activates a user context. A
// newer pending attempt or connection conflict retains the quarantined proof.
func (r *CompanyInterfaceLifecycle) PreserveRecoveredInstallation(ctx context.Context, expected ci.LocalRecovery, c ci.Reconciliation, recovered ci.LocalInstallation) error {
	if !validLocalConfirmation(expected, c) || c.State != "approved" || c.GrantRevoked || !matchesInstallationConfirmation(recovered, c) || !ci.ValidSecret(recovered.Credential) || expected.CentralOrigin != r.central {
		return ci.ErrDenied
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE FOR UPDATE`).Scan(&revision); e != nil {
		return e
	}
	if revision != expected.Request.ConnectionRevision {
		return ci.ErrConflict
	}
	hash := ci.Digest(expected.Request.LocalSession)
	var newer bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM company_interface_local_contexts WHERE local_session_hash=$1 AND ((pending_attempt IS NOT NULL AND pending_attempt<>$2) OR ciphertext IS NOT NULL))`, hash[:], expected.Request.AttemptID).Scan(&newer); e != nil {
		return e
	}
	if newer {
		return ci.ErrConflict
	}
	var id string
	if e = tx.QueryRowContext(ctx, `SELECT id::text FROM company_interface_local_recovery WHERE id=$1 FOR UPDATE`, expected.Request.AttemptID).Scan(&id); e != nil {
		return ci.ErrDenied
	}
	binding, storedHash, storedRevision, encrypted, e := readLocalRecovery(ctx, tx, id)
	if e != nil {
		return e
	}
	var actual ci.LocalRecovery
	if e = r.cipher.Open(encrypted, localRecoveryAAD(storedHash, binding.ContextEpoch, storedRevision, id, binding.CentralOrigin), &actual); e != nil {
		return e
	}
	expected.State = ""
	if ci.Fingerprint(expected) != ci.Fingerprint(actual) {
		return ci.ErrConflict
	}
	tokens := ci.Tokens{GrantID: recovered.GrantID, GrantEpoch: recovered.GrantEpoch, Company: recovered.Company, InstallationHash: recovered.InstallationHash, CentralOrigin: recovered.CentralOrigin, LocalOrigin: recovered.LocalOrigin, InstallationCredential: recovered.Credential}
	if e = r.saveLocalInstallation(ctx, tx, expected.Request, tokens); e != nil {
		return e
	}
	return tx.Commit()
}
