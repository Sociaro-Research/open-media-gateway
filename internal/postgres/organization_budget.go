package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"time"
)

func readOrganizationBudget(ctx context.Context, q rowQuerier, owner, key string, at time.Time, exclude string, lock bool) (*money.USD, *money.USD, error) {
	var credit money.USD
	var starts time.Time
	sqlText := `SELECT credit_limit_micros,created_at FROM organizations WHERE id=$1`
	if lock {
		sqlText += ` FOR NO KEY UPDATE`
	}
	if e := q.QueryRowContext(ctx, sqlText, owner).Scan(&credit, &starts); e != nil {
		return nil, nil, e
	}
	var actor, mode string
	var weekly *money.USD
	e := q.QueryRowContext(ctx, `SELECT k.user_id,k.resource_mode,m.weekly_limit_micros FROM user_api_keys k JOIN organization_members m ON m.organization_id=k.organization_id AND m.user_id=k.user_id AND m.removed_at IS NULL AND m.role<>'content_officer' WHERE k.id=$1 AND k.organization_id=$2`, key, owner).Scan(&actor, &mode, &weekly)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil, core.ErrKeyUnavailable
	}
	if e != nil {
		return nil, nil, e
	}
	if e := checkBudgetKey(ctx, q, owner, key, lock); e != nil {
		return nil, nil, e
	}
	// Personally funded provider requests do not spend the organization's shared balance.
	if mode != "shared" {
		return nil, nil, nil
	}
	var credits, consumed, weekConsumed money.USD
	e = q.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(amount_micros) FROM organization_credits WHERE organization_id=$1),0)::bigint,COALESCE(SUM(CEIL((`+taskConsumedSQL+`)*1000000)),0)::bigint,COALESCE(SUM(CEIL((`+taskConsumedSQL+`)*1000000)) FILTER(WHERE actor_id::text=$4 AND created_at>=`+weekStartSQL+`),0)::bigint FROM tasks WHERE owner_id=$1 AND created_at>=$2 AND id::text<>$3 AND provider_owner_id='00000000-0000-0000-0000-000000000001'`, owner, starts, exclude, actor).Scan(&credits, &consumed, &weekConsumed)
	if e != nil {
		return nil, nil, e
	}
	available := credits + credit - consumed
	remaining := &available
	if weekly != nil {
		availableWeekly := *weekly - weekConsumed
		if availableWeekly < *remaining {
			remaining = &availableWeekly
		}
	}
	return weekly, remaining, nil
}
