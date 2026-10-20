package core

import "context"

// ProviderDirectory resolves owner-scoped connections on demand in both processes.
// Provider returns an error matching ErrProviderUnavailable for a confirmed
// missing/unsupported connection. Other lookup errors leave accepted work retryable.
type ProviderDirectory interface {
	Providers(context.Context) (map[string]MediaProvider, error)
	Provider(context.Context, string) (MediaProvider, error)
}

type providerRevisionKey struct{}
type manualCallKey struct{}

func WithProviderRevision(ctx context.Context, revision string) context.Context {
	return context.WithValue(ctx, providerRevisionKey{}, revision)
}
func ProviderRevision(ctx context.Context) string {
	v, _ := ctx.Value(providerRevisionKey{}).(string)
	return v
}

// Manual calls have already been validated against an administrator's model schema.
func WithManualCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, manualCallKey{}, true)
}
func IsManualCall(ctx context.Context) bool {
	v, _ := ctx.Value(manualCallKey{}).(bool)
	return v
}

func (s *TaskService) SetProviderDirectory(d ProviderDirectory) { s.directory = d }
func (s *TaskService) providerFor(ctx context.Context, id string) (MediaProvider, bool) {
	if s.directory != nil {
		p, err := s.directory.Provider(ctx, id)
		return p, err == nil
	}
	p, ok := s.providers[id]
	return p, ok
}
