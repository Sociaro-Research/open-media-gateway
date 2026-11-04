package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

// Explicit admission sites call this before their domain locks/writes. The
// strongest settings mode must already be held by callers that later need UPDATE.
// Order: settings -> company -> sorted users -> source -> grant -> family -> key
// -> membership. Locks last only until local admission commits, never provider I/O.
func guardCompanyMutation(ctx context.Context, tx *sql.Tx, company string, targets ...string) error {
	a, delegated := ci.MutationAuthorityFromContext(ctx)
	if !delegated {
		if identity.CompanyBoundary(ctx) != "" {
			return ci.ErrDenied
		}
		return nil
	}
	p, effective := a.Original, a.Effective
	mode := installation.FromContext(ctx)
	if mode.Role != "central" || mode.CentralOrigin != p.Grant.CentralOrigin || identity.CompanyBoundary(ctx) != p.Grant.Company || company != "" && company != p.Grant.Company || identity.ActorID(ctx) != effective.Actor || !ci.ValidID(p.Grant.ID) || !ci.ValidID(p.Grant.Company) {
		return ci.ErrDenied
	}
	var settings bool
	if err := tx.QueryRowContext(ctx, `SELECT id FROM system_settings WHERE id=TRUE FOR SHARE`).Scan(&settings); err != nil {
		return err
	}
	if err := ciOrgLock(ctx, tx, p.Grant.Company); err != nil {
		return err
	}
	// Resolve target membership before taking any target user's domain lock. Removed
	// recipients remain revocable under the original issuance/revocation policy.
	for _, target := range targets {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND user_id::text=$2)`, p.Grant.Company, target).Scan(&member); err != nil {
			return err
		}
		if !member {
			return ci.ErrDenied
		}
	}
	ids := append([]string{p.Actor, effective.Actor}, targets...)
	if p.BrowserActor != "" {
		ids = append(ids, p.BrowserActor)
	}
	sort.Strings(ids)
	prior := ""
	for _, id := range ids {
		if id == prior {
			continue
		}
		prior = id
		if !ci.ValidID(id) {
			return ci.ErrDenied
		}
		var manager bool
		if err := tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&manager); err != nil {
			return companyMutationReadError(err)
		}
		if manager && (id == p.Actor || id == effective.Actor || id == p.BrowserActor) {
			return ci.ErrDenied
		}
	}
	source := ""
	if p.BrowserActor != "" {
		if !ci.ValidID(p.SessionID) {
			return ci.ErrDenied
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_session_id::text,'') FROM company_interface_sessions WHERE id=$1 AND actor_id=$2`, p.SessionID, p.BrowserActor).Scan(&source); err != nil {
			return companyMutationReadError(err)
		}
		if !ci.ValidID(source) {
			return ci.ErrDenied
		}
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM company_interface_source_sessions WHERE id=$1 AND actor_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR SHARE`, source, p.BrowserActor).Scan(&id); err != nil {
			return companyMutationReadError(err)
		}
	}
	grant, err := ciReadGrant(ctx, tx, p.Grant.ID)
	if err != nil {
		return companyMutationReadError(err)
	}
	if grant.Validate(time.Now()) != nil || grant.Company != p.Grant.Company || grant.Epoch != p.Grant.Epoch || grant.CentralOrigin != p.Grant.CentralOrigin || grant.LocalOrigin != p.Grant.LocalOrigin {
		return ci.ErrDenied
	}
	var hash []byte
	if err = tx.QueryRowContext(ctx, `SELECT credential_hash FROM company_interface_grants WHERE id=$1`, grant.ID).Scan(&hash); err != nil {
		return companyMutationReadError(err)
	}
	if !bytes.Equal(hash, a.InstallationHash[:]) {
		return ci.ErrDenied
	}
	if p.BrowserActor != "" {
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT id::text FROM company_interface_sessions WHERE id=$1 AND grant_id=$2 AND organization_id=$3 AND actor_id=$4 AND source_session_id=$5 AND grant_epoch=$6 AND session_epoch=$7 AND token_version=$8 AND access_hash=$9 AND local_session_hash=$10 AND revoked_at IS NULL AND access_expires_at>clock_timestamp() FOR UPDATE`, p.SessionID, grant.ID, grant.Company, p.BrowserActor, source, p.Grant.Epoch, p.SessionEpoch, p.SessionVersion, a.AccessHash[:], a.LocalSessionHash[:]).Scan(&id); err != nil {
			return companyMutationReadError(err)
		}
	} else if p.SessionID != "" || p.Kind != ci.CentralKey {
		return ci.ErrDenied
	}
	if a.KeyPresent {
		var scopes []byte
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT id::text,scopes FROM user_api_keys WHERE id=$1 AND user_id=$2 AND organization_id=$3 AND token_hash=$4 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) FOR UPDATE`, p.KeyID, p.Actor, grant.Company, a.KeyHash[:]).Scan(&id, &scopes); err != nil {
			return companyMutationReadError(err)
		}
		var current []string
		if json.Unmarshal(scopes, &current) != nil {
			return ci.ErrDenied
		}
		if effective.Kind == ci.CentralKey && a.Scope != "" {
			found := false
			for _, scope := range current {
				if scope == a.Scope {
					found = true
				}
			}
			if !found {
				return ci.ErrDenied
			}
		}
	} else if p.KeyID != "" || p.Kind == ci.CentralKey {
		return ci.ErrDenied
	}
	roles := map[string]string{p.Actor: p.Role, effective.Actor: effective.Role}
	if p.BrowserActor != "" {
		if _, ok := roles[p.BrowserActor]; !ok {
			roles[p.BrowserActor] = p.BrowserRole
		}
	}
	for actor, want := range roles {
		var role string
		if err = tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL FOR SHARE`, grant.Company, actor).Scan(&role); err != nil {
			return companyMutationReadError(err)
		}
		if want != "" && role != want {
			return ci.ErrDenied
		}
	}
	return nil
}

func keyRecipient(owner string, key apikey.Key) string {
	if key.UserID != "" {
		return key.UserID
	}
	return owner
}

func companyMutationReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ci.ErrDenied
	}
	return err
}
func companyMutationDomainError(err, denied error) error {
	if errors.Is(err, ci.ErrDenied) {
		return denied
	}
	return err
}
