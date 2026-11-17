package api

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// unavailableReferenceTarget explains a failed referenceTargetModel lookup. A
// model sent without its provider is a request mistake, not a missing model.
func unavailableReferenceTarget(target *referencevalidation.Target) error {
	if target.Provider == "" && target.Model != "" {
		return referencevalidation.Invalid("References", "provider is required next to model. Send the provider of this model from GET /v1/models.")
	}
	return referencevalidation.Invalid("References", "The selected model is unavailable to this account or key. Choose an available model.")
}

// Resolve only within the caller's authorized catalog and selected connection.
// Public IDs are opaque: custom aliases and slash-bearing native IDs must not
// be split or have a provider prefix stripped. Retain native IDs for old clients,
// but fail closed if an alias also names another native model on this connection.
func referenceTargetModel(catalog []core.ProviderDescriptor, provider, id string) (core.MediaModel, bool) {
	if provider == "" || id == "" {
		return core.MediaModel{}, false
	}
	var result core.MediaModel
	found := false
	for _, p := range catalog {
		if p.ID != provider {
			continue
		}
		for _, m := range p.Models {
			publicID := m.PublicID
			if publicID == "" {
				publicID = p.ID + "/" + m.ID
			}
			if id != m.ID && id != publicID {
				continue
			}
			if found {
				return core.MediaModel{}, false
			}
			result, found = m, true
		}
	}
	return result, found
}
