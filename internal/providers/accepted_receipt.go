package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func (p *managedProvider) ValidateAcceptedReceipt(ctx context.Context, task core.Task) error {
	if task.Provider != p.id || core.ProviderRevision(ctx) != task.ProviderIdentity || task.OwnerID != identity.OwnerID(ctx) || task.ProviderOwnerID != identity.ProviderOwnerID(ctx) || task.StorageOwnerID != identity.StorageOwnerID(ctx) {
		return core.ErrProviderChanged
	}
	// Resolve the immutable accepted credentials using the same owner and
	// historical-revision rules as GetTask. Never quote or query upstream here.
	_, err := p.manager.resolve(ctx, p.id, false, task.ProviderIdentity)
	return err
}
