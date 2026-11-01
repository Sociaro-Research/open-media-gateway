package core

import "context"

// ProviderConnectionScope is injected by the host after resolving an authorized
// concrete connection. It is not a credential/configuration field or public
// request option. Adapter-owned handles must bind ID as well as owner/revision;
// two connections with identical keys must remain different scopes.
type ProviderConnectionScope struct {
	ID       string
	OwnerID  string
	Revision string
}
type providerConnectionScopeKey struct{}

func WithProviderConnectionScope(ctx context.Context, scope ProviderConnectionScope) context.Context {
	return context.WithValue(ctx, providerConnectionScopeKey{}, scope)
}
func ProviderConnectionScopeFromContext(ctx context.Context) (ProviderConnectionScope, bool) {
	s, ok := ctx.Value(providerConnectionScopeKey{}).(ProviderConnectionScope)
	return s, ok && s.ID != "" && s.OwnerID != "" && s.Revision != ""
}
