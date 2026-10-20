package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/limits"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
)

type APIKeyRepository struct{ db *sql.DB }

func NewAPIKeyRepository(db *sql.DB) *APIKeyRepository { return &APIKeyRepository{db: db} }

func (r *APIKeyRepository) Create(ctx context.Context, owner string, key apikey.Key, hash []byte) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = createAPIKey(ctx, tx, owner, key, hash); err != nil {
		return err
	}
	return tx.Commit()
}

func createAPIKey(ctx context.Context, tx *sql.Tx, owner string, key apikey.Key, hash []byte) error {
	var err error
	issuer := owner
	if key.UserID != "" {
		owner = key.UserID
	}
	if key.OrganizationID == "" && owner != issuer {
		return apikey.ErrForbidden
	}

	if key.OrganizationID != "" {
		// Serialize issuance with membership changes and check both people in this transaction.
		var ignored string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, key.OrganizationID).Scan(&ignored); err != nil {
			return err
		}
		role, e := organizationAccess(ctx, tx, issuer, key.OrganizationID)
		if e != nil {
			return apikey.ErrForbidden
		}
		var recipientRole string
		if e = tx.QueryRowContext(ctx, `SELECT m.role FROM organization_members m JOIN users u ON u.id=m.user_id WHERE m.organization_id=$1 AND m.user_id=$2 AND m.removed_at IS NULL AND NOT u.is_client_manager`, key.OrganizationID, owner).Scan(&recipientRole); e != nil {
			return apikey.ErrForbidden
		}
		if !org.IssueKey(role, issuer, recipientRole, owner) {
			return apikey.ErrForbidden
		}
	}

	var manager bool
	if err = tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&manager); err != nil {
		return err
	}
	if manager {
		return apikey.ErrNotFound
	}
	// The owner row lock serializes concurrent key creation, including across
	// gateway replicas. Expired/revoked keys do not count; existing keys survive
	// a subsequent reduction of the configured maximum.
	policy, err := readRequestLimits(ctx, tx, key.OrganizationID)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_api_keys WHERE user_id=$1 AND organization_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp())`, owner, key.OrganizationID).Scan(&count); err != nil {
		return err
	}
	if count >= policy.Effective["user_active_keys"] {
		return limits.ErrActiveKeys
	}
	if err = setAuditActor(ctx, tx, issuer); err != nil {
		return err
	}
	scopes, err := json.Marshal(key.Scopes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_api_keys
		(id, user_id, name, prefix, token_hash, scopes, created_at, expires_at, resource_mode,storage_mode,organization_id,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, key.ID, owner, key.Name, key.Prefix, hash, scopes, key.CreatedAt, key.ExpiresAt, resourceMode(key.ResourceMode), resourceMode(key.StorageMode), nullableString(key.OrganizationID), issuer)
	if err != nil {
		return err
	}
	return nil
}

func (r *APIKeyRepository) List(ctx context.Context, owner string) ([]apikey.Key, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, prefix, scopes, created_at, expires_at, last_used_at, revoked_at, resource_mode,storage_mode,COALESCE(organization_id::text,''),user_id,created_by,(SELECT email FROM users WHERE id=user_id),(SELECT email FROM users WHERE id=created_by)
		FROM user_api_keys WHERE user_id = $1 ORDER BY created_at DESC, id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []apikey.Key{}
	for rows.Next() {
		var key apikey.Key
		var scopes []byte
		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &scopes, &key.CreatedAt, &key.ExpiresAt, &key.LastUsedAt, &key.RevokedAt, &key.ResourceMode, &key.StorageMode, &key.OrganizationID, &key.UserID, &key.CreatedBy, &key.UserEmail, &key.CreatorEmail); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(scopes, &key.Scopes); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r *APIKeyRepository) Revoke(ctx context.Context, owner, id string) error {
	// Retain the historical owner-only contract used by internal callers.
	var found bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_api_keys WHERE id=$1 AND user_id=$2)`, id, owner).Scan(&found); err != nil {
		return err
	}
	if !found {
		return apikey.ErrNotFound
	}
	return r.RevokeForActor(ctx, owner, id)
}

func (r *APIKeyRepository) Authenticate(ctx context.Context, hash []byte) (identity.User, apikey.Key, error) {
	var user identity.User
	var key apikey.Key
	var scopes []byte
	// Check revocation and expiration on every request; no authentication cache.
	err := r.db.QueryRowContext(ctx, `SELECT u.id, u.email, u.display_name, COALESCE(u.picture_url, ''), k.id, k.scopes,k.resource_mode,k.storage_mode,COALESCE(k.organization_id::text,''),COALESCE(m.role IN ('admin','supermanager','content_officer'),false),COALESCE(m.role,'')
		FROM user_api_keys k JOIN users u ON u.id = k.user_id LEFT JOIN organization_members m ON m.organization_id=k.organization_id AND m.user_id=k.user_id AND m.removed_at IS NULL
		WHERE NOT u.is_client_manager AND (k.organization_id IS NULL OR m.user_id IS NOT NULL) AND k.token_hash = $1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > NOW())`, hash).
		Scan(&user.ID, &user.Email, &user.Name, &user.Picture, &key.ID, &scopes, &key.ResourceMode, &key.StorageMode, &key.OrganizationID, &key.OrganizationAdmin, &key.OrganizationRole)
	if errors.Is(err, sql.ErrNoRows) {
		return user, key, apikey.ErrNotFound
	}
	if err != nil {
		return user, key, err
	}
	if err := json.Unmarshal(scopes, &key.Scopes); err != nil {
		return user, key, err
	}
	// Usage metadata is approximate and must not turn every poll into a write.
	_, _ = r.db.ExecContext(ctx, `UPDATE user_api_keys SET last_used_at = NOW()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, key.ID)
	return user, key, nil
}

func (r *APIKeyRepository) Usage(ctx context.Context, owner string, month time.Time) ([]apikey.MonthlyUsage, error) {
	return r.usage(ctx, owner, "", false, month)
}
func (r *APIKeyRepository) OrganizationUsage(ctx context.Context, actor, organization string, month time.Time) ([]apikey.MonthlyUsage, error) {
	role, err := organizationAccess(ctx, r.db, actor, organization)
	if err != nil {
		return nil, apikey.ErrForbidden
	}
	return r.usage(ctx, actor, organization, org.Statistics(role), month)
}
func (r *APIKeyRepository) usage(ctx context.Context, owner, organization string, allUsers bool, month time.Time) ([]apikey.MonthlyUsage, error) {
	start, until := keyUsagePeriod(month)
	rows, err := r.db.QueryContext(ctx, `WITH monthly_tasks AS (
 SELECT api_key_id, status, `+taskConsumedSQL+` AS charged,
 COALESCE((cost->>'metered_usd')::numeric, CASE WHEN cost->>'actual_source'='free_provider' THEN 0::numeric WHEN provider_usage->>'currency'='USD' AND jsonb_typeof(provider_usage->'estimated_cost')='number' THEN (provider_usage->>'estimated_cost')::numeric END) AS metered,
 CASE WHEN cost->>'actual_source'='free_provider' THEN 0::numeric ELSE (cost->>'actual_usd')::numeric END AS actual,
 COALESCE((cost->>'customer_expected_usd')::numeric,(cost->>'expected_usd')::numeric) AS expected,
 `+taskConsumedSQL+` AS consumed,
 CASE WHEN (cost->>'billed_units')::numeric > 0 AND cost->>'tariff_usd' IS NOT NULL
 THEN (cost->>'tariff_usd')::numeric * COALESCE((cost->>'package_units')::numeric,0) / (cost->>'billed_units')::numeric ELSE 0 END AS covered
 FROM tasks WHERE ($5 OR COALESCE(actor_id,owner_id)::text=$1) AND ($4='' OR owner_id::text=$4) AND api_key_id IS NOT NULL AND created_at >= $2 AND created_at < $3
 ) SELECT k.id, COALESCE(SUM(t.metered),0), COUNT(t.api_key_id),
 COUNT(t.api_key_id) FILTER (WHERE t.status IN ('queued','running')),
 COUNT(t.api_key_id) FILTER (WHERE t.charged IS NULL AND t.status NOT IN ('queued','running')),
 COALESCE(SUM(CEIL(t.actual*1000000)),0)::bigint,
 COALESCE(SUM(CEIL(t.expected*1000000)),0)::bigint,
 COUNT(t.api_key_id) FILTER (WHERE t.actual IS NULL),
 COALESCE(SUM(CEIL(t.consumed*1000000)),0)::bigint, COALESCE(SUM(CEIL(t.covered*1000000)),0)::bigint
 FROM user_api_keys k LEFT JOIN monthly_tasks t ON t.api_key_id=k.id
 WHERE ($5 OR k.user_id::text=$1) AND ($4='' OR k.organization_id::text=$4) GROUP BY k.id ORDER BY k.created_at DESC,k.id`, owner, start, until, organization, allUsers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []apikey.MonthlyUsage{}
	for rows.Next() {
		item := apikey.MonthlyUsage{Month: start.Format("2006-01")}
		if err := rows.Scan(&item.KeyID, &item.EstimatedCostUSD, &item.Tasks, &item.PendingTasks, &item.UnknownCostTasks, &item.ActualUSD, &item.ExpectedUSD, &item.UnconfirmedTasks, &item.ConsumedUSD, &item.PackageCoveredUSD); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func resourceMode(value string) string {
	if value == "" {
		return "personal"
	}
	return value
}

// Calendar boundaries retain the requested location, including DST transitions.
func keyUsagePeriod(month time.Time) (time.Time, time.Time) {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	return start, start.AddDate(0, 1, 0)
}
