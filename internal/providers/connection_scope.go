package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (p *managedProvider) connectionScope(ctx context.Context, adapter core.MediaProvider, revision string) context.Context {
	if revision == "" {
		revision = core.ProviderRevision(ctx)
	}
	if revision == "" {
		if a, ok := adapter.(*connectionAdapter); ok {
			revision = connectionIdentity(p.id, a.definition, a.credentials)
		}
	}
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: p.id, OwnerID: identity.ProviderOwnerID(ctx), Revision: revision})
}

func (p *managedProvider) OpenArtifact(ctx context.Context, reference string) (core.ArtifactSource, error) {
	adapter, err := p.manager.resolve(ctx, p.id, false, core.ProviderRevision(ctx))
	if err != nil {
		return core.ArtifactSource{}, err
	}
	opener, ok := adapter.(core.ArtifactOpener)
	if !ok {
		return core.ArtifactSource{}, core.ErrProviderUnavailable
	}
	return opener.OpenArtifact(p.connectionScope(ctx, adapter, core.ProviderRevision(ctx)), reference)
}
func (a *connectionAdapter) OpenArtifact(ctx context.Context, reference string) (core.ArtifactSource, error) {
	opener, ok := a.MediaProvider.(core.ArtifactOpener)
	if !ok {
		return core.ArtifactSource{}, core.ErrProviderUnavailable
	}
	source, err := opener.OpenArtifact(ctx, reference)
	return source, a.redact(err)
}

// ResolveResourceConnection obtains current host-authorized connection identity
// for resource admission. The caller cannot supply its revision or owner.
func (m *Manager) ResolveResourceConnection(ctx context.Context, id string) (core.ProviderConnectionScope, error) {
	adapter, err := m.resolve(ctx, id, true)
	if err != nil {
		return core.ProviderConnectionScope{}, err
	}
	connection, ok := adapter.(*connectionAdapter)
	if !ok {
		return core.ProviderConnectionScope{}, core.ErrProviderUnavailable
	}
	return core.ProviderConnectionScope{ID: id, OwnerID: identity.ProviderOwnerID(ctx), Revision: connectionIdentity(id, connection.definition, connection.credentials)}, nil
}
