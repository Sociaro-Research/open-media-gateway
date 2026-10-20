package payments

import (
	"context"
	"log/slog"
	"time"
)

type ReconciliationRepository interface {
	ClaimPaymentChecks(context.Context) ([]string, error)
	SchedulePaymentCheck(context.Context, string, time.Duration) error
}

// Run repairs missed webhooks and retries delayed fees after restarts. Database
// leases coordinate replicas; the ledger lock still guarantees single posting.
func (m *Manager) Run(ctx context.Context) {
	go m.runStablecoinChecks(ctx)
	repo, ok := m.Repo.(ReconciliationRepository)
	if !ok {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		m.sweep(ctx, repo)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (m *Manager) sweep(ctx context.Context, repo ReconciliationRepository) {
	if err := m.ExpirePayments(ctx); err != nil {
		if ctx.Err() == nil {
			slog.Warn("payment request expiry will retry")
		}
		return
	}
	ids, e := repo.ClaimPaymentChecks(ctx)
	if e != nil {
		if ctx.Err() == nil {
			slog.Warn("payment reconciliation queue unavailable")
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		check, cancel := context.WithTimeout(ctx, 12*time.Second)
		p, e := m.Reconcile(check, id)
		delay := time.Minute
		if e == nil && (p.Settled || p.Status == "expired" || p.Status == "failed") {
			delay = 6 * time.Hour
			if p.InvoiceMode != "local" && p.Settled && p.TaxMode == "exclusive" && (p.Invoice == nil || p.Invoice.PDFURL == "") {
				delay = time.Minute
			}
		}
		if e != nil {
			slog.Warn("payment verification will retry", "payment_id", id)
		}
		cancel()
		if e := repo.SchedulePaymentCheck(ctx, id, delay); e != nil && ctx.Err() == nil {
			slog.Warn("payment reconciliation scheduling failed", "payment_id", id)
		}
	}
}

// Independent scheduling keeps a delayed card-provider request from delaying
// the once-per-minute chain scan. Database watcher locks coordinate replicas.
func (m *Manager) runStablecoinChecks(ctx context.Context) {
	stable, ok := m.Repo.(StablecoinRepository)
	if !ok || m.Chain == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err := stable.ScanStablecoins(check, m.Chain); err != nil && ctx.Err() == nil {
			slog.Warn("stablecoin verification will retry")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
