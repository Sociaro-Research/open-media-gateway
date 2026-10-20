package postgres

import (
	"context"
	"encoding/json"

	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

func (r *SystemRepository) BalanceHistory(ctx context.Context, actor, organization string, offset int) ([]payments.BalanceEntry, error) {
	role, err := r.OrganizationAccess(ctx, actor, organization)
	if err != nil {
		return nil, err
	}
	if !org.Statistics(role) {
		return nil, org.ErrForbidden
	}
	// Only actual balance changes belong in history. Payment attempts remain in
	// payments for reconciliation, but must not appear or affect pagination.
	rows, err := r.db.QueryContext(ctx, `SELECT c.id,c.created_at,c.amount_micros,c.kind,c.reason,COALESCE(pr.name,''),to_jsonb(p)
 FROM organization_credits c LEFT JOIN payments p ON p.id=c.payment_id LEFT JOIN payment_providers pr ON pr.id=p.provider_id
 WHERE c.organization_id=$1 AND c.amount_micros<>0 AND (p.id IS NULL OR p.live OR $2)
 ORDER BY c.created_at DESC,c.id LIMIT 51 OFFSET $3`, organization, role == "system_admin", offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []payments.BalanceEntry{}
	for rows.Next() {
		var entry payments.BalanceEntry
		var payment []byte
		if err = rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Amount, &entry.Kind, &entry.Reason, &entry.Provider, &payment); err != nil {
			return nil, err
		}
		if len(payment) > 0 {
			if err = json.Unmarshal(payment, &entry.Payment); err != nil {
				return nil, err
			}
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
