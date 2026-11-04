package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"slices"
	"sort"
	"time"
)

func checkPreparedProvider(ctx context.Context, tx *sql.Tx, task core.Task) error {
	if task.ProviderCatalogFingerprint != "" {
		if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))`, "catalog-admission:"+task.Provider); e != nil {
			return e
		}
		var raw []byte
		e := tx.QueryRowContext(ctx, `SELECT payload FROM catalog_sync_state WHERE provider=$1 FOR SHARE`, task.Provider).Scan(&raw)
		state := catalogsync.State{}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			if e = json.Unmarshal(raw, &state); e != nil {
				return e
			}
		}
		if catalogsync.AdmissionFingerprint(state) != task.ProviderCatalogFingerprint {
			return core.ErrProviderChanged
		}
	}

	var enabled, archived bool
	var version int64
	e := tx.QueryRowContext(ctx, `SELECT enabled,archived,version FROM provider_settings WHERE owner_id=$1 AND provider_id=$2 FOR SHARE`, resourceOwner(task.ProviderOwnerID, identity.ProviderOwnerID(ctx)), task.Provider).Scan(&enabled, &archived, &version)
	if errors.Is(e, sql.ErrNoRows) && task.ProviderSettingsVersion == 0 {
		return nil
	} // Static non-managed adapters.
	if e != nil {
		return core.ErrProviderUnavailable
	}
	if !enabled || archived {
		return core.ErrProviderUnavailable
	}
	if task.ProviderSettingsVersion != 0 && version != task.ProviderSettingsVersion {
		return core.ErrProviderChanged
	}
	return nil
}

// One sorted lock set for every inspected source, including override audio.
// Deletion takes the same lock before tombstoning and scanning active tasks.
func checkPreparedReferences(ctx context.Context, tx *sql.Tx, task core.Task) error {
	bindings := append([]core.ReferenceBinding(nil), task.Options.ReferenceSources...)
	if a := task.Options.OverrideSound; a != nil {
		owner := a.DataOwnerID
		if owner == "" {
			owner = a.OwnerID
		}
		bindings = append(bindings, core.ReferenceBinding{OwnerID: a.OwnerID, ReferenceID: a.ReferenceID, StorageOwnerID: owner, StorageKey: a.StorageKey, SHA256: a.SHA256, SizeBytes: a.SizeBytes})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].OwnerID == bindings[j].OwnerID {
			return bindings[i].ReferenceID < bindings[j].ReferenceID
		}
		return bindings[i].OwnerID < bindings[j].OwnerID
	})
	for _, b := range bindings {
		if b.OwnerID != identity.OwnerID(ctx) && b.OwnerID != templates.LibraryOwnerID {
			return core.ErrOptionsInvalid
		}
		if b.ReferenceID == "" || b.StorageKey == "" {
			return core.ErrOptionsInvalid
		}
		if e := sourceAssetLock(ctx, tx, b.OwnerID, b.ReferenceID); e != nil {
			return e
		}
		author := identity.ContentAuthorFilter(ctx)
		if b.OwnerID == templates.LibraryOwnerID {
			author = ""
		}
		var valid bool
		e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE owner_id::text=$1 AND id::text=$2 AND NOT deleting AND COALESCE(storage_owner_id::text,owner_id::text)=$3 AND storage_key=$4 AND COALESCE(content_sha256,'')=$5 AND size_bytes=$6 AND ($7='' OR actor_id::text=$7))`, b.OwnerID, b.ReferenceID, b.StorageOwnerID, b.StorageKey, b.SHA256, b.SizeBytes, author).Scan(&valid)
		if e != nil {
			return e
		}
		if !valid {
			return core.ErrOptionsInvalid
		}
	}
	return nil
}

func (r *TaskRepository) ValidateProject(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return projectAccess(ctx, r.db, id)
}

// Uses the same company -> user -> key -> membership order as management writes.
// No external I/O occurs while these locks are held.
func lockGenerationKey(ctx context.Context, tx *sql.Tx, providerOwner string) error {
	key := identity.APIKeyID(ctx)
	if key == nil {
		return nil
	}
	company := identity.OrganizationFromContext(ctx).ID
	if company != "" {
		var id string
		if e := tx.QueryRowContext(ctx, `SELECT id::text FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, company).Scan(&id); e != nil {
			return core.ErrKeyUnavailable
		}
	}
	var manager bool
	if e := tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1 FOR SHARE`, identity.ActorID(ctx)).Scan(&manager); e != nil || manager {
		return core.ErrKeyUnavailable
	}
	var actor, org, mode string
	var scopes []byte
	var revoked, expires *time.Time
	e := tx.QueryRowContext(ctx, `SELECT user_id::text,COALESCE(organization_id::text,''),resource_mode,scopes,revoked_at,expires_at FROM user_api_keys WHERE id=$1 FOR UPDATE`, *key).Scan(&actor, &org, &mode, &scopes, &revoked, &expires)
	if e != nil || actor != identity.ActorID(ctx) || org != company || revoked != nil || expires != nil && !expires.After(time.Now()) {
		return core.ErrKeyUnavailable
	}
	var allowed []string
	if json.Unmarshal(scopes, &allowed) != nil || !slices.Contains(allowed, apikey.Generation) {
		return core.ErrKeyUnavailable
	}
	provider := actor
	if mode == "shared" {
		provider = identity.SharedOwnerID
	}
	if provider != resourceOwner(providerOwner, identity.ProviderOwnerID(ctx)) {
		return core.ErrKeyUnavailable
	}
	if company != "" {
		var role string
		e = tx.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 AND removed_at IS NULL FOR SHARE`, company, actor).Scan(&role)
		if e != nil || role == "content_officer" || identity.OrganizationFromContext(ctx).Role != "" && role != identity.OrganizationFromContext(ctx).Role {
			return core.ErrKeyUnavailable
		}
	}
	return nil
}
