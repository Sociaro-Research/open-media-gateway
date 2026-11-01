package core

import "context"

// AcceptedReceiptValidator reopens the server-owned connection snapshot before
// replaying an accepted result without a provider query. It performs no paid
// submission or network access. Historical accepted revisions follow the same
// ownership rules as ordinary polling and authenticated artifact reads.
type AcceptedReceiptValidator interface {
	ValidateAcceptedReceipt(context.Context, Task) error
}
