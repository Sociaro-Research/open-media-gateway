package postgres

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (r *TaskRepository) CostReport(ctx context.Context, f core.ReportFilter) ([]core.CostReportRow, error) {
	result := []core.CostReportRow{}
	err := r.walkCostReport(ctx, f, func(row core.CostReportRow) error { result = append(result, row); return nil })
	return result, err
}
func (r *TaskRepository) WalkCostReport(ctx context.Context, f core.ReportFilter, visit func(core.CostReportRow) error) error {
	f.Limit = 0
	f.Offset = 0
	return r.walkCostReport(ctx, f, visit)
}
func (r *TaskRepository) walkCostReport(ctx context.Context, f core.ReportFilter, visit func(core.CostReportRow) error) error {
	rows, err := r.db.QueryContext(ctx, `WITH filtered AS (
 SELECT * FROM tasks WHERE (($7 AND EXISTS(SELECT 1 FROM organizations WHERE id=tasks.owner_id)) OR (NOT $7 AND owner_id=$1)) AND ($8='' OR COALESCE(actor_id,owner_id)::text=$8) AND ($9='' OR actor_id::text=$9) AND ($10='' OR lower(COALESCE(tasks.identity_snapshot->>'actor_email',(SELECT email FROM users WHERE id=COALESCE(tasks.actor_id,tasks.owner_id))))=lower($10)) AND created_at >= $2 AND created_at < $3 AND ($4='' OR api_key_id::text=$4) AND ($11='' OR id::text=$11)
 ), priced AS (SELECT *, CEIL((`+taskConsumedSQL+`)*1000000)::bigint AS consumed FROM filtered)
 SELECT t.id,t.created_at,t.api_key_id,COALESCE(t.identity_snapshot->>'api_key_name',k.name,CASE WHEN t.api_key_id IS NULL THEN 'Console' ELSE 'Unknown key' END),t.provider,t.model,t.status,t.cost,t.consumed,COALESCE(t.actor_id,t.owner_id),COALESCE(t.identity_snapshot->>'actor_email',u.email,''),t.provider_usage,COALESCE(t.identity_snapshot->>'organization_id',o.id::text,''),COALESCE(t.identity_snapshot->>'organization_name',o.name,''),t.public_model,t.identity_snapshot IS NOT NULL,t.identity_snapshot,COALESCE(t.identity_snapshot->>'provider_name',t.provider),p.profit_micros,
 COALESCE(p.state,CASE WHEN o.id IS NULL OR t.provider_owner_id<>'00000000-0000-0000-0000-000000000001' THEN 'not_applicable' WHEN t.created_at<(SELECT starts_at FROM sales_bonus_settings WHERE id=TRUE) THEN 'not_tracked' WHEN t.status IN ('queued','running') THEN 'generating' ELSE 'awaiting_cost' END),p.recognized_at,COALESCE(u.picture_url,'')
 FROM priced t LEFT JOIN generation_profits p ON p.task_id=t.id LEFT JOIN user_api_keys k ON k.id=t.api_key_id AND k.user_id=COALESCE(t.actor_id,t.owner_id)
 LEFT JOIN organizations o ON o.id=t.owner_id
 LEFT JOIN users u ON u.id=COALESCE(t.actor_id,t.owner_id)
 ORDER BY t.created_at DESC,t.id DESC LIMIT NULLIF($5,0) OFFSET $6`, identity.OwnerID(ctx), f.From, f.Until, f.KeyID, f.Limit, f.Offset, f.AllOrganizations, f.UserID, identity.FinancialAuthorFilter(ctx), f.UserEmail, f.GenerationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row core.CostReportRow
		var cost, usage, snapshot []byte
		if err = rows.Scan(&row.ID, &row.CreatedAt, &row.KeyID, &row.KeyName, &row.Provider, &row.Model, &row.Status, &cost, &row.ConsumedUSD, &row.UserID, &row.UserEmail, &usage, &row.OrganizationID, &row.OrganizationName, &row.PublicModel, &row.IdentityHistorical, &snapshot, &row.ProviderName, &row.ProfitUSD, &row.ProfitStatus, &row.ProfitRecognizedAt, &row.UserPicture); err != nil {
			return err
		}
		row.IdentitySnapshot = snapshot
		if len(cost) > 0 {
			if err = json.Unmarshal(cost, &row.Cost); err != nil {
				return err
			}
		}
		if len(usage) > 0 {
			if err = json.Unmarshal(usage, &row.Usage); err != nil {
				return err
			}
		}
		if err = visit(row); err != nil {
			return err
		}
	}
	return rows.Err()
}
