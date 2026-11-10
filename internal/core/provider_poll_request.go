package core

import "context"

type providerPollRequestKey struct{}

// WithProviderPollRequest supplies immutable accepted request context to native
// pollers. It preserves native task IDs for recovery and invoice matching.
// This is server-side context, never a client capability or account attestation.
func WithProviderPollRequest(ctx context.Context, r MediaRequest) context.Context {
	r.Prompt = ""
	return context.WithValue(ctx, providerPollRequestKey{}, r)
}

func ProviderPollRequestFromContext(ctx context.Context) (MediaRequest, bool) {
	r, ok := ctx.Value(providerPollRequestKey{}).(MediaRequest)
	return r, ok
}
