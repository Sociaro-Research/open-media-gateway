package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

// Transaction-local attribution cannot bleed into the next pooled connection.
func beginAuditTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	actor := ""
	if u, ok := identity.UserFromContext(ctx); ok {
		actor = u.ID
	}
	key := ""
	if k := identity.APIKeyID(ctx); k != nil {
		key = *k
	}
	correlation := audit.Correlation(ctx)
	if correlation == "" {
		correlation = uuid.NewString()
	}
	_, err = tx.ExecContext(ctx, `SELECT set_config('gateway.audit_actor',$1,true),set_config('gateway.audit_key',$2,true),set_config('gateway.audit_correlation',$3,true),set_config('gateway.audit_reason','',true)`, actor, key, correlation)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func setAuditActor(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('gateway.audit_actor',$1,true)`, id)
	return err
}
func setAuditRevocationReason(ctx context.Context, tx *sql.Tx, reason string) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('gateway.audit_reason',$1,true)`, reason)
	return err
}
func auditSystem(ctx context.Context, tx *sql.Tx, actor, action, target string, details any) error {
	// Server/bootstrap callers have no browser context. Other callers already set
	// attribution during the administrator permission check.
	if start := strings.LastIndex(actor, "["); start >= 0 && strings.HasSuffix(actor, "]") {
		actor = actor[start+1 : len(actor)-1]
	} else {
		actor = ""
	}
	kind := action
	if action == "administrator_role" || action == "user_role" || action == "client_manager_role" || strings.HasPrefix(action, "administrator_") {
		kind = "user"
	}
	return appendAudit(ctx, tx, "", actor, action, kind, target, details)
}
func orgEvent(ctx context.Context, tx *sql.Tx, org, actor, action string, details any) error {
	if action == "payment_settlement" { // reconciliation is a service action, with an initiating user.
		data := map[string]any{"initiated_by": actor, "settlement": details}
		return appendAudit(ctx, tx, org, identity.SystemOwnerID, action, "payment", auditTarget(details, org), data)
	}
	return appendAudit(ctx, tx, org, actor, action, "organization", auditTarget(details, org), details)
}
func auditTarget(details any, fallback string) string {
	data, _ := json.Marshal(details)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	for _, k := range []string{"payment_id", "key_id", "id", "user_id"} {
		if s, ok := fields[k].(string); ok && s != "" {
			return s
		}
	}
	return fallback
}
func appendAudit(ctx context.Context, tx *sql.Tx, org, actor, action, kind, target string, details any) error {
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT audit_record($1,$2,$3,$4,$5,'',$6::jsonb)`, org, actor, action, kind, target, data)
	return err
}

const auditColumns = `id,created_at,scope,schema_version,organization_id,organization_name,actor_id,actor_name,actor_email,actor_type,calling_key_id,action,object_type,object_id,object_name,outcome,correlation_id,details,legacy_source,legacy_id,COALESCE((SELECT picture_url FROM users WHERE id::text=audit_events.actor_id),''),CASE WHEN object_type='user' THEN COALESCE((SELECT picture_url FROM users WHERE id::text=audit_events.object_id),'') ELSE '' END`

func scanAudit(row rowScanner) (audit.Event, error) {
	var e audit.Event
	err := row.Scan(&e.ID, &e.At, &e.Scope, &e.SchemaVersion, &e.OrganizationID, &e.OrganizationName, &e.ActorID, &e.ActorName, &e.ActorEmail, &e.ActorType, &e.CallingKeyID, &e.Action, &e.ObjectType, &e.ObjectID, &e.ObjectName, &e.Outcome, &e.CorrelationID, &e.Details, &e.LegacySource, &e.LegacyID, &e.ActorPicture, &e.ObjectPicture)
	return e, err
}
func (r *SystemRepository) AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+auditColumns+` FROM audit_events WHERE ($1='' OR organization_id=$1) AND ($2='' OR actor_id=$2 OR actor_email ILIKE '%'||$2||'%' OR actor_name ILIKE '%'||$2||'%') AND ($3='' OR action=$3) AND ($4='' OR object_type=$4) AND ($5='' OR object_id=$5) AND ($6='' OR outcome=$6) AND ($7='' OR correlation_id=$7) AND created_at >= $8 AND created_at < $9 AND ($10=0 OR id<$10) AND ($12='' OR scope=$12) ORDER BY id DESC LIMIT $11`, f.OrganizationID, f.ActorID, f.Action, f.ObjectType, f.ObjectID, f.Outcome, f.CorrelationID, f.From, f.Until, f.BeforeID, f.Limit, f.Scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []audit.Event{}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (r *SystemRepository) AuditEvent(ctx context.Context, id int64, org string) (audit.Event, error) {
	return scanAudit(r.db.QueryRowContext(ctx, `SELECT `+auditColumns+` FROM audit_events WHERE id=$1 AND ($2='' OR organization_id=$2)`, id, org))
}
func (r *SystemRepository) RevokedKeys(ctx context.Context, f audit.Filter) ([]audit.RevokedKey, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT h.key_id,h.organization_id,h.revoked_at,h.snapshot,
 COALESCE(SUM(CEIL((`+taskConsumedSQL+`)*1000000)),0)::bigint,
 COUNT(t.id) FILTER(WHERE t.status IN ('queued','running')),
 COUNT(t.id) FILTER(WHERE t.status NOT IN ('queued','running') AND (`+taskConsumedSQL+`) IS NULL)
 FROM revoked_key_history h LEFT JOIN tasks t ON t.api_key_id::text=h.key_id
 WHERE ($1='' OR h.organization_id=$1) AND h.revoked_at >= $2 AND h.revoked_at < $3
 AND ($4='' OR h.snapshot->>'revoked_by'=$4 OR h.snapshot->>'revoker_email' ILIKE '%'||$4||'%' OR h.snapshot->>'revoker_name' ILIKE '%'||$4||'%') AND ($5='' OR h.key_id=$5)
 GROUP BY h.key_id ORDER BY h.revoked_at DESC,h.key_id LIMIT $6 OFFSET $7`, f.OrganizationID, f.From, f.Until, f.ActorID, f.ObjectID, f.Limit, f.BeforeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []audit.RevokedKey{}
	for rows.Next() {
		var k audit.RevokedKey
		if err := rows.Scan(&k.ID, &k.OrganizationID, &k.RevokedAt, &k.Snapshot, &k.CurrentSpentMicros, &k.PendingRequests, &k.UnknownCostRequests); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Failed management operations are recorded separately after the mutation has
// rolled back. Never retain request bodies, URLs, tokens or provider error text.
func (r *SystemRepository) AuditFailure(ctx context.Context, org, action, kind, id string, status int) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	outcome := "failed"
	if status == 403 {
		outcome = "denied"
	}
	_, err = tx.ExecContext(ctx, `SELECT audit_record($1,'',$2,$3,$4,'',$5::jsonb,$6)`, org, action, kind, id, `{"http_status":`+strconv.Itoa(status)+`}`, outcome)
	if err != nil {
		return err
	}
	return tx.Commit()
}
