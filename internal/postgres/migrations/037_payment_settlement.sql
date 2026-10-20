-- +goose Up
ALTER TABLE payments ADD COLUMN settlement JSONB CHECK(settlement IS NULL OR jsonb_typeof(settlement)='object');
-- Refresh existing Stripe payments to populate their actual settlement currency.
-- Metadata enrichment does not create another ledger credit or success notice.
UPDATE payments p SET next_reconcile_at=LEAST(next_reconcile_at,NOW())
FROM payment_providers pr WHERE p.provider_id=pr.id AND pr.kind='stripe' AND p.settled;
-- +goose Down
ALTER TABLE payments DROP COLUMN settlement;
