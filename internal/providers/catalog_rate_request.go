package providers

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

var errCatalogRateRequestUnavailable = errors.New("provider does not define typed catalogue quote fixtures")

type catalogRateRequestProvider interface {
	CatalogRateRequest(core.MediaModel, catalogsync.Rate) (core.MediaRequest, error)
}

func (a *connectionAdapter) CatalogRateRequest(model core.MediaModel, rate catalogsync.Rate) (core.MediaRequest, error) {
	if provider, ok := a.MediaProvider.(catalogRateRequestProvider); ok {
		return provider.CatalogRateRequest(model, rate)
	}
	return core.MediaRequest{}, errCatalogRateRequestUnavailable
}
