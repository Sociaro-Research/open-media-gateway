package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/google/uuid"
)

// CompanyInterfaceLifecycle is an isolated central protocol repository. It has
// no HTTP routes, capability advertisement or connection-mode activation yet.
// central is trusted deployment configuration, never a request-selected URL.
type CompanyInterfaceLifecycle struct {
	db         *sql.DB
	central    string
	cipher     *ci.ReceiptCipher
	playground *playgroundkey.Manager
	// Test-only fault injection, deliberately not exported to transport/config.
	failWrite func(string) error
}

func NewCompanyInterfaceLifecycle(db *sql.DB, master, central string) (*CompanyInterfaceLifecycle, error) {
	normalized, e := installation.NormalizeOrigin(central)
	if e != nil || normalized != central || !strings.HasPrefix(central, "https://") {
		return nil, ci.ErrDenied
	}
	c, e := ci.NewReceiptCipher(master)
	if e != nil {
		return nil, e
	}
	keys := NewAPIKeyRepository(db)
	p, e := playgroundkey.New(keys, keys, master)
	if e != nil {
		return nil, e
	}
	return &CompanyInterfaceLifecycle{db: db, central: central, cipher: c, playground: p}, nil
}
func (r *CompanyInterfaceLifecycle) host(ctx context.Context, tx *sql.Tx) error {
	m := installation.FromContext(ctx)
	if m.Role != "central" || m.CentralOrigin != r.central {
		return ci.ErrDenied
	}
	_, e := readSystemSettings(ctx, tx, true)
	return e
}
func (r *CompanyInterfaceLifecycle) checkpoint(name string) error {
	if r.failWrite != nil {
		return r.failWrite(name)
	}
	return nil
}
func ciInstallationLock(ctx context.Context, tx *sql.Tx, hash [32]byte) error {
	_, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,89))`, hex.EncodeToString(hash[:]))
	return e
}
func ciActor(ctx context.Context) (identity.User, error) {
	u, ok := identity.UserFromContext(ctx)
	if !ok || identity.APIKeyID(ctx) != nil || !ci.ValidID(u.ID) || !ci.ValidID(u.SourceSessionID) {
		return u, ci.ErrDenied
	}
	return u, nil
}

// Call after the organization lock when one exists. Password reset locks users
// before deleting source sessions, so issuance/refresh uses that same order.
func ciLockActor(ctx context.Context, tx *sql.Tx, u identity.User) (time.Time, error) {
	var manager bool
	if e := tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&manager); e != nil || manager {
		return time.Time{}, ci.ErrDenied
	}
	var expiry time.Time
	e := tx.QueryRowContext(ctx, `SELECT expires_at FROM company_interface_source_sessions WHERE id=$1 AND actor_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR SHARE`, u.SourceSessionID, u.ID).Scan(&expiry)
	if e != nil {
		return expiry, ci.ErrDenied
	}
	return expiry, setAuditActor(ctx, tx, u.ID)
}
func ciOrgLock(ctx context.Context, tx *sql.Tx, company string) error {
	var id string
	e := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, company).Scan(&id)
	if e != nil {
		return ci.ErrDenied
	}
	return nil
}
func ciRole(ctx context.Context, tx *sql.Tx, company, actor string) (string, error) {
	var role string
	e := tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL`, company, actor).Scan(&role)
	if e != nil || !org.ValidRole(role) {
		return "", ci.ErrDenied
	}
	return role, nil
}

type ciAttempt struct {
	binding                                                          ci.Attempt
	hash, approvalHash, receipt                                      []byte
	code, state, actor, source, approvalOp, redeemOp, grant, session string
	expires, recovery                                                time.Time
}

const ciAttemptColumns = `binding,request_hash,user_code,state,expires_at,recovery_expires_at,COALESCE(actor_id::text,''),COALESCE(source_session_id::text,''),COALESCE(approval_operation::text,''),approval_hash,COALESCE(redeem_operation::text,''),COALESCE(grant_id::text,''),COALESCE(session_id::text,''),receipt`

func scanCIAttempt(row interface{ Scan(...any) error }) (a ciAttempt, e error) {
	var raw []byte
	e = row.Scan(&raw, &a.hash, &a.code, &a.state, &a.expires, &a.recovery, &a.actor, &a.source, &a.approvalOp, &a.approvalHash, &a.redeemOp, &a.grant, &a.session, &a.receipt)
	if e != nil {
		return a, ci.ErrDenied
	}
	if json.Unmarshal(raw, &a.binding) != nil {
		return a, ci.ErrDenied
	}
	fp := ci.Fingerprint(a.binding)
	if !bytes.Equal(a.hash, fp[:]) {
		return a, ci.ErrDenied
	}
	return a, nil
}
func (a ciAttempt) challenge() ci.Challenge {
	return ci.Challenge{ID: a.binding.ID, Code: a.code, State: a.state, ExpiresAt: a.expires}
}
func (r *CompanyInterfaceLifecycle) Start(ctx context.Context, in ci.Start) (ci.Challenge, error) {
	if ci.Negotiate(in.Protocol) != nil {
		return ci.Challenge{}, ci.ErrProtocol
	}
	local, e := installation.NormalizeOrigin(in.LocalOrigin)
	if e != nil || local != in.LocalOrigin || !ci.ValidID(in.AttemptID) || !ci.ValidSecret(in.InstallationProof) || !ci.ValidSecret(in.Proof) || !ci.ValidSecret(in.LocalSession) || in.ConnectionRevision < 0 || len(in.Label) > 120 {
		return ci.Challenge{}, ci.ErrDenied
	}
	switch in.Mode {
	case "connect", "login", "rebind", "legacy-upgrade":
	default:
		return ci.Challenge{}, ci.ErrDenied
	}
	if in.PreviousGrantID != "" && !ci.ValidID(in.PreviousGrantID) {
		return ci.Challenge{}, ci.ErrDenied
	}
	a := ci.BindStart(r.central, in)
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return ci.Challenge{}, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return ci.Challenge{}, e
	}
	if e = ciInstallationLock(ctx, tx, a.InstallationHash); e != nil {
		return ci.Challenge{}, e
	}
	fp := ci.Fingerprint(a)
	raw, _ := json.Marshal(a)
	codeSecret, e := ci.NewSecret()
	if e != nil {
		return ci.Challenge{}, e
	}
	code := strings.ToUpper(hex.EncodeToString([]byte(codeSecret)[:8]))
	_, e = tx.ExecContext(ctx, `INSERT INTO company_interface_attempts(id,installation_hash,request_hash,binding,user_code,expires_at,recovery_expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+INTERVAL '10 minutes',clock_timestamp()+INTERVAL '7 days') ON CONFLICT(id) DO NOTHING`, a.ID, a.InstallationHash[:], fp[:], raw, code)
	if e != nil {
		return ci.Challenge{}, e
	}
	current, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE id=$1 FOR UPDATE`, a.ID))
	if e != nil {
		return ci.Challenge{}, e
	}
	if !bytes.Equal(current.hash, fp[:]) {
		return ci.Challenge{}, ci.ErrConflict
	}
	if e = tx.Commit(); e != nil {
		return ci.Challenge{}, e
	}
	return current.challenge(), nil
}

// Describe binds the attempt to the authenticated central browser account and
// source session before displaying its code/origin/consent. Switching accounts
// or source sessions requires a distinct attempt, never silent reassignment.
func (r *CompanyInterfaceLifecycle) Describe(ctx context.Context, code string) (ci.Challenge, ci.Attempt, error) {
	u, e := ciActor(ctx)
	if e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE user_code=$1 FOR UPDATE`, code))
	if e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	if _, e = ciLockActor(ctx, tx, u); e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	if a.actor != "" && (a.actor != u.ID || a.source != u.SourceSessionID) {
		return ci.Challenge{}, ci.Attempt{}, ci.ErrConflict
	}
	if a.state != "pending" || !time.Now().Before(a.expires) {
		return ci.Challenge{}, ci.Attempt{}, ci.ErrDenied
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_attempts SET actor_id=$2,source_session_id=$3 WHERE id=$1`, a.binding.ID, u.ID, u.SourceSessionID); e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	if e = tx.Commit(); e != nil {
		return ci.Challenge{}, ci.Attempt{}, e
	}
	return a.challenge(), a.binding, nil
}

func ciReadGrant(ctx context.Context, tx *sql.Tx, id string) (ci.Grant, error) {
	var g ci.Grant
	var hash []byte
	e := tx.QueryRowContext(ctx, `SELECT id::text,installation_hash,organization_id::text,central_origin,approved_local_origin,purpose,protocol_version,revocation_epoch,expires_at,revoked_at IS NOT NULL FROM company_interface_grants WHERE id=$1 FOR UPDATE`, id).Scan(&g.ID, &hash, &g.Company, &g.CentralOrigin, &g.LocalOrigin, &g.Purpose, &g.Version, &g.Epoch, &g.ExpiresAt, &g.Revoked)
	if e != nil || len(hash) != 32 {
		return g, ci.ErrDenied
	}
	copy(g.InstallationHash[:], hash)
	return g, nil
}
func ciRevokeGrant(ctx context.Context, tx *sql.Tx, id string) error {
	if _, e := tx.ExecContext(ctx, `UPDATE company_interface_grants SET revoked_at=clock_timestamp(),revocation_epoch=revocation_epoch+1 WHERE id=$1 AND revoked_at IS NULL`, id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE grant_id=$1 AND revoked_at IS NULL`, id)
	return e
}
func ciReceiptBinding(a ciAttempt) any {
	return []any{ci.Purpose, ci.Version, "approval", a.binding.ID, a.approvalOp, a.hash, a.approvalHash, a.actor, a.source}
}

// Approve performs every initial provisioning write in ONE transaction, including
// the encrypted exact issued result. Its response contains no credentials.
func (r *CompanyInterfaceLifecycle) Approve(ctx context.Context, in ci.Approval) (ci.Challenge, error) {
	u, e := ciActor(ctx)
	if e != nil || !in.Consent || !ci.ValidID(in.OperationID) {
		return ci.Challenge{}, ci.ErrDenied
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return ci.Challenge{}, e
	}
	defer tx.Rollback()
	if e = r.host(ctx, tx); e != nil {
		return ci.Challenge{}, e
	}
	// The settings lock serializes provisioning with legacy company creation.
	a, e := scanCIAttempt(tx.QueryRowContext(ctx, `SELECT `+ciAttemptColumns+` FROM company_interface_attempts WHERE user_code=$1 FOR UPDATE`, in.Code))
	if e != nil {
		return ci.Challenge{}, e
	}
	if a.actor != u.ID || a.source != u.SourceSessionID {
		return ci.Challenge{}, ci.ErrConflict
	}
	if e = ciInstallationLock(ctx, tx, a.binding.InstallationHash); e != nil {
		return ci.Challenge{}, e
	}
	approvalHash := ci.Fingerprint([]any{a.hash, u.ID, u.SourceSessionID, in})
	var company, central string
	e = tx.QueryRowContext(ctx, `SELECT organization_id::text,central_origin FROM company_interface_installations WHERE installation_hash=$1`, a.binding.InstallationHash[:]).Scan(&company, &central)
	existing := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return ci.Challenge{}, e
	}
	if existing && central != r.central {
		return ci.Challenge{}, ci.ErrDenied
	}
	legacy := false
	if !existing {
		var legacyCompany string
		e = tx.QueryRowContext(ctx, `SELECT organization_id::text FROM workspace_installations WHERE installation_hash=$1`, a.binding.InstallationHash[:]).Scan(&legacyCompany)
		if e == nil {
			if a.binding.Mode != "legacy-upgrade" {
				return ci.Challenge{}, ci.ErrDenied
			}
			company = legacyCompany
			legacy = true
		} else if !errors.Is(e, sql.ErrNoRows) {
			return ci.Challenge{}, e
		}
	}
	if company != "" {
		if e = ciOrgLock(ctx, tx, company); e != nil {
			return ci.Challenge{}, e
		}
	}
	sourceExpiry, e := ciLockActor(ctx, tx, u)
	if e != nil {
		return ci.Challenge{}, e
	}
	if a.state == "approved" {
		if a.approvalOp != in.OperationID || !bytes.Equal(a.approvalHash, approvalHash[:]) || !time.Now().Before(a.recovery) {
			return ci.Challenge{}, ci.ErrConflict
		}
		if _, e = r.liveIssued(ctx, tx, a); e != nil {
			return ci.Challenge{}, e
		}
		return a.challenge(), tx.Commit()
	}
	if a.state != "pending" || !time.Now().Before(a.expires) {
		return ci.Challenge{}, ci.ErrDenied
	}
	initial := !existing && !legacy
	if initial && a.binding.Mode != "connect" {
		return ci.Challenge{}, ci.ErrDenied
	}
	if !initial {
		role, err := ciRole(ctx, tx, company, u.ID)
		if err != nil {
			return ci.Challenge{}, err
		}
		if a.binding.Mode != "login" && role != "supermanager" {
			return ci.Challenge{}, ci.ErrDenied
		}
	}
	if initial {
		if a.binding.PreviousGrantID != "" {
			return ci.Challenge{}, ci.ErrConflict
		}
		company = uuid.NewString()
		name := org.CompanyName(a.binding.Label) + " · " + hex.EncodeToString(a.binding.InstallationHash[:16])
		if e = checkCompanyIdentity(ctx, tx, u.ID, "", name, ""); e != nil {
			return ci.Challenge{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2)`, company, name); e != nil {
			return ci.Challenge{}, e
		}
		if e = r.checkpoint("company"); e != nil {
			return ci.Challenge{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, u.ID); e != nil {
			return ci.Challenge{}, e
		}
		if e = r.checkpoint("membership"); e != nil {
			return ci.Challenge{}, e
		}
	}
	if !existing {
		if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_installations(installation_hash,organization_id,central_origin) VALUES($1,$2,$3)`, a.binding.InstallationHash[:], company, r.central); e != nil {
			return ci.Challenge{}, e
		}
		if e = r.checkpoint("installation"); e != nil {
			return ci.Challenge{}, e
		}
	}
	var grant ci.Grant
	grantCredential := ""
	if existing {
		if !ci.ValidID(a.binding.PreviousGrantID) {
			return ci.Challenge{}, ci.ErrConflict
		}
		grant, e = ciReadGrant(ctx, tx, a.binding.PreviousGrantID)
		if e != nil || grant.InstallationHash != a.binding.InstallationHash || grant.Company != company || grant.CentralOrigin != r.central {
			return ci.Challenge{}, ci.ErrDenied
		}
		var latest string
		if e = tx.QueryRowContext(ctx, `SELECT id::text FROM company_interface_grants WHERE installation_hash=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, a.binding.InstallationHash[:]).Scan(&latest); e != nil || latest != grant.ID {
			return ci.Challenge{}, ci.ErrConflict
		}
		if a.binding.Mode == "login" {
			var hash []byte
			e = tx.QueryRowContext(ctx, `SELECT credential_hash FROM company_interface_grants WHERE id=$1`, grant.ID).Scan(&hash)
			if e != nil || !bytes.Equal(hash, a.binding.GrantHash[:]) || grant.Validate(time.Now()) != nil || grant.LocalOrigin != a.binding.LocalOrigin {
				return ci.Challenge{}, ci.ErrDenied
			}
		} else {
			if a.binding.Mode != "connect" && a.binding.Mode != "rebind" {
				return ci.Challenge{}, ci.ErrDenied
			}
			if a.binding.Mode == "connect" && (!grant.Revoked || grant.LocalOrigin != a.binding.LocalOrigin) {
				return ci.Challenge{}, ci.ErrDenied
			}
			if a.binding.Mode == "rebind" && grant.LocalOrigin == a.binding.LocalOrigin {
				return ci.Challenge{}, ci.ErrConflict
			}
			if e = ciRevokeGrant(ctx, tx, grant.ID); e != nil {
				return ci.Challenge{}, e
			}
			if e = r.checkpoint("previous_grant_revocation"); e != nil {
				return ci.Challenge{}, e
			}
		}
	}
	if !existing || a.binding.Mode != "login" {
		grantCredential, e = ci.NewSecret()
		if e != nil {
			return ci.Challenge{}, e
		}
		hash := ci.Digest(grantCredential)
		var epoch int64
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revocation_epoch),0)+1 FROM company_interface_grants WHERE installation_hash=$1`, a.binding.InstallationHash[:]).Scan(&epoch); e != nil {
			return ci.Challenge{}, e
		}
		grant = ci.Grant{ID: uuid.NewString(), InstallationHash: a.binding.InstallationHash, Company: company, CentralOrigin: r.central, LocalOrigin: a.binding.LocalOrigin, Purpose: ci.Purpose, Version: ci.Version, Epoch: epoch}
		if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_grants(id,installation_hash,organization_id,central_origin,approved_local_origin,purpose,protocol_version,credential_hash,revocation_epoch) VALUES($1,$2,$3,$4,$5,'company-interface',1,$6,$7)`, grant.ID, grant.InstallationHash[:], company, r.central, grant.LocalOrigin, hash[:], epoch); e != nil {
			return ci.Challenge{}, e
		}
		if e = r.checkpoint("grant"); e != nil {
			return ci.Challenge{}, e
		}
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
	access, e := ci.NewSecret()
	if e != nil {
		return ci.Challenge{}, e
	}
	refresh, e := ci.NewSecret()
	if e != nil {
		return ci.Challenge{}, e
	}
	ah, rh := ci.Digest(access), ci.Digest(refresh)
	tokens := ci.Tokens{AttemptID: a.binding.ID, GrantID: grant.ID, Company: company, Actor: u.ID, SessionID: uuid.NewString(), LocalOrigin: a.binding.LocalOrigin, CentralOrigin: r.central, InstallationCredential: grantCredential, AccessToken: access, RefreshToken: refresh, InstallationHash: a.binding.InstallationHash, LocalSessionHash: a.binding.LocalSessionHash, ConnectionRevision: a.binding.ConnectionRevision, GrantEpoch: grant.Epoch, SessionEpoch: 1, Version: 1, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry}
	if _, e = tx.ExecContext(ctx, `INSERT INTO company_interface_sessions(id,grant_id,organization_id,actor_id,local_session_hash,grant_epoch,access_hash,access_expires_at,refresh_hash,refresh_expires_at,source_session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tokens.SessionID, grant.ID, company, u.ID, a.binding.LocalSessionHash[:], grant.Epoch, ah[:], accessExpiry, rh[:], refreshExpiry, u.SourceSessionID); e != nil {
		return ci.Challenge{}, e
	}
	if e = r.checkpoint("session"); e != nil {
		return ci.Challenge{}, e
	}
	// Only the initial explicit connection prepares a key. Additional user login
	// never invokes key creation and works for read-only/member roles.
	if initial || legacy {
		key, _, prepared, err := r.playground.Prepare(u.ID, company)
		if err != nil {
			return ci.Challenge{}, err
		}
		secret, err := r.playground.DecodeRecord(prepared)
		if err != nil {
			return ci.Challenge{}, err
		}
		record, err := createPlaygroundKey(ctx, tx, u.ID, company, a.binding.ID, key, apikey.Hash(secret), prepared.Ciphertext, r.checkpoint)
		if err != nil {
			return ci.Challenge{}, err
		}
		tokens.PlaygroundKey, err = r.playground.DecodeRecord(record)
		if err != nil {
			return ci.Challenge{}, err
		}
		// Reusing a legacy managed slot must enforce Manager.open's authority
		// contract inside this same transaction before sealing its secret.
		var validManaged bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_api_keys WHERE id=$1 AND user_id=$2 AND organization_id=$3 AND purpose=$4 AND scopes='["generation"]'::jsonb AND token_hash=$5 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()))`, record.KeyID, u.ID, company, playgroundkey.Purpose, apikey.Hash(tokens.PlaygroundKey)).Scan(&validManaged); e != nil || !validManaged {
			return ci.Challenge{}, ci.ErrDenied
		}
		if e = r.checkpoint("managed_key"); e != nil {
			return ci.Challenge{}, e
		}
	}
	a.approvalOp = in.OperationID
	a.approvalHash = approvalHash[:]
	receipt, e := r.cipher.Seal(tokens, ciReceiptBinding(a))
	if e != nil {
		return ci.Challenge{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE company_interface_attempts SET state='approved',approval_operation=$2,approval_hash=$3,grant_id=$4,session_id=$5,receipt=$6 WHERE id=$1`, a.binding.ID, in.OperationID, approvalHash[:], grant.ID, tokens.SessionID, receipt); e != nil {
		return ci.Challenge{}, e
	}
	if e = r.checkpoint("receipt"); e != nil {
		return ci.Challenge{}, e
	}
	if e = tx.Commit(); e != nil {
		return ci.Challenge{}, e
	}
	a.state = "approved"
	return a.challenge(), nil
}
