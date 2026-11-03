package postgres

import (
	"context"
	"database/sql"
	"errors"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
)

func (r *CompanyInterfaceLifecycle) CoordinatorPending(ctx context.Context, local string) (ci.Start, int64, bool, error) {
	hash := ci.Digest(local)
	var exists bool
	e := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM company_interface_local_recovery WHERE local_session_hash=$1 AND state IN ('pending','quarantined'))`, hash[:]).Scan(&exists)
	if e != nil || !exists {
		return ci.Start{}, 0, exists, e
	}
	in, epoch, e := r.LocalPending(ctx, local)
	return in, epoch, true, e
}

// LocalPreparation reuses the installation-wide origin-keyed proof. Local
// administrator authority only controls installation setup, never central roles.
func (r *CompanyInterfaceLifecycle) LocalPreparation(ctx context.Context, owner, mode string) (ci.Start, error) {
	var in ci.Start
	if e := r.db.QueryRowContext(ctx, `SELECT revision FROM company_interface_local_control WHERE singleton=TRUE`).Scan(&in.ConnectionRevision); e != nil {
		return in, e
	}
	binding, _, e := readLocalInstallation(ctx, r.db)
	exists := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return in, e
	}
	if mode == "login" && !exists {
		return in, ci.ErrDenied
	}
	if mode != "login" {
		allowed, err := NewSystemRepository(r.db).IsAdmin(ctx, owner)
		if err != nil {
			return in, err
		}
		if !allowed {
			return in, ci.ErrDenied
		}
	}
	var encrypted []byte
	if exists {
		stored, err := r.LocalInstallation(ctx)
		if err != nil {
			return in, err
		}
		in.PreviousGrantID = stored.GrantID
		in.GrantCredential = stored.Credential
		if e = r.db.QueryRowContext(ctx, `SELECT proof_ciphertext FROM remote_installation_identities WHERE origin=$1`, r.central).Scan(&encrypted); e != nil {
			return in, ci.ErrDenied
		}
	} else {
		if mode == "rebind" {
			return in, ci.ErrDenied
		}
		proof, err := ci.NewSecret()
		if err != nil {
			return in, err
		}
		candidate, err := r.proofCipher.Encrypt([]byte(proof))
		if err != nil {
			return in, err
		}
		encrypted, e = NewSystemRepository(r.db).InstallationIdentity(ctx, owner, r.central, candidate)
		if e != nil {
			return in, e
		}
	}
	raw, e := r.proofCipher.Decrypt(encrypted)
	if e != nil || !ci.ValidSecret(string(raw)) {
		return in, ci.ErrDenied
	}
	in.InstallationProof = string(raw)
	if exists && ci.InstallationDigest(in.InstallationProof) != binding.InstallationHash {
		return ci.Start{}, ci.ErrDenied
	}
	return in, nil
}

var _ ci.LocalStore = (*CompanyInterfaceLifecycle)(nil)
