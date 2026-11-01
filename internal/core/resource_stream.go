package core

import "context"

// ResourceSourceOpener opens only an already inspected local reference snapshot
// under the accepted data owner. URLs and client assertions cannot choose a host.
type ResourceSourceOpener interface {
	OpenResourceSource(context.Context, ResourceSourceInput) (ArtifactSource, error)
}
type resourceSourceOpenerKey struct{}

func WithResourceSourceOpener(ctx context.Context, opener ResourceSourceOpener) context.Context {
	return context.WithValue(ctx, resourceSourceOpenerKey{}, opener)
}
func ResourceSourceOpenerFromContext(ctx context.Context) (ResourceSourceOpener, bool) {
	opener, ok := ctx.Value(resourceSourceOpenerKey{}).(ResourceSourceOpener)
	return opener, ok && opener != nil
}
