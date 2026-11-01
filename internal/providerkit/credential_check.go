package providerkit

import (
	"context"
	"time"
)

// CredentialCheck records the scope-limited result of an explicit account query.
// It is runtime state, not model entitlement, generation readiness or a receipt.
type CredentialCheck struct {
	CheckedAt time.Time
	LatencyMS int64
	Err       error
	Warning   string // Safe explanation of unavailable optional details, never raw provider text.
}

type credentialCheckKey struct{}

// WithCredentialCheck separates account queries from generation/poll overrides.
func WithCredentialCheck(ctx context.Context) context.Context {
	return context.WithValue(ctx, credentialCheckKey{}, true)
}
func IsCredentialCheck(ctx context.Context) bool {
	value, _ := ctx.Value(credentialCheckKey{}).(bool)
	return value
}
