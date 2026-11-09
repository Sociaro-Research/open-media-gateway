package providers

import (
	"context"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Resolve the same historical owner/credential revision as polling. Reading a
// retained descriptor never re-quotes or re-admits the accepted task.
func (p *managedProvider) HistoricalUsageModel(ctx context.Context, id string) (core.MediaModel, bool, error) {
	adapter, err := p.manager.resolve(ctx, p.id, false, core.ProviderRevision(ctx))
	if err != nil {
		return core.MediaModel{}, false, err
	}
	history, ok := adapter.(core.HistoricalUsageCatalog)
	if !ok {
		return core.MediaModel{}, false, nil
	}
	return history.HistoricalUsageModel(p.connectionScope(ctx, adapter, core.ProviderRevision(ctx)), id)
}
