package payments

import "time"

// StatusView projects expiration without changing payment, invoice or ledger
// state. Keep this predicate aligned with the background expiry sweep. A
// terminal cancellation/failure and an already settled payment stay terminal.
func StatusView(p Payment, now time.Time) Payment {
	if (p.Status == "pending" || p.Status == "confirming") && p.RequestExpired(now) {
		p.Status = "expired"
		p.CheckoutURL = ""
	}
	return p
}
