package api

import (
	"context"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// A file upload/validation target contains public local binding IDs, not trusted
// resource snapshots. Resolve those IDs with the current owner's actual
// connection before normalizing a model that also needs an existing session.
// Admission independently resolves and validates them again before submission.
func (s *Server) referenceValidationResources(ctx context.Context, provider string, model core.MediaModel, options core.MediaOptions) (core.MediaOptions, error) {
	options.ResourceSnapshots = nil
	if err := core.ValidateResourceReferenceIntent(model, options); err != nil {
		return core.MediaOptions{}, referencevalidation.Invalid("References", "Selected resource references are invalid for this model.")
	}
	if len(options.ResourceReferences) == 0 {
		return options, nil
	}
	if s.resourceRepository == nil || s.resourceScopes == nil {
		return core.MediaOptions{}, referencevalidation.Invalid("References", "Selected resources are unavailable. Review the resource bindings.")
	}
	resolved, err := providerresources.New(s.resourceRepository, s.resourceScopes).ResolveResourceReferences(ctx, provider, options.ResourceReferences)
	if err != nil {
		return core.MediaOptions{}, referencevalidation.Invalid("References", "Selected resources are unavailable to this owner or connection. Review the resource bindings.")
	}
	options.ResourceSnapshots = resolved
	return options, nil
}
