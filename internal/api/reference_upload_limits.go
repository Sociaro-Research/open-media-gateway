package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// The resolver uses the current authenticated catalog, including key/model
// visibility. A client target selects that catalog entry; it never defines limits.
func (s *Server) referenceUploadContext(r *http.Request) context.Context {
	return references.WithUploadLimitResolver(r.Context(), func(ctx context.Context, target *referencevalidation.Target) (int64, error) {
		return s.referenceUploadLimit(r.WithContext(ctx), target)
	})
}

func (s *Server) referenceUploadLimit(r *http.Request, target *referencevalidation.Target) (int64, error) {
	if target == nil {
		return references.MaxBytes, nil
	}
	model, ok := templateModel(s.visibleCatalog(r), target.Provider, target.Model)
	if !ok {
		return 0, referencevalidation.Invalid("References", "The selected model is unavailable to this account or key. Choose an available model.")
	}
	if target.Role != "" && target.Role != "reference_voice" {
		return 0, referencevalidation.Invalid("References", "Unknown reference role.")
	}
	if target.Role == "reference_voice" || !model.SupportsVideoInput {
		return references.MaxBytes, nil
	}
	modeFound, videoMode := false, false
	for _, mode := range core.ReferenceModesFor(model) {
		if target.Options.ReferenceMode != "" && mode.ID != target.Options.ReferenceMode {
			continue
		}
		modeFound = true
		if mode.MaxVideos > 0 {
			videoMode = true
		}
	}
	if !modeFound {
		return 0, referencevalidation.Invalid("References", "The selected reference workflow is unavailable.")
	}
	if !videoMode {
		return references.MaxBytes, nil
	}
	limit := referencevalidation.ReviewedVideoMaxBytes(model)
	if model.ReferenceMediaLimits != nil {
		rule := model.ReferenceMediaLimits.Video
		if rule.MaxBytes > 0 {
			native := rule.MaxBytes
			if !rule.MaxBytesInclusive {
				native--
			}
			if native < limit {
				limit = native
			}
		}
	}
	if limit <= 0 {
		return 0, referencevalidation.Invalid("References", "The selected model does not permit a nonempty video file.")
	}
	return limit, nil
}

// Resource creation has a distinct reviewed source schema. Ordinary generation
// limits (including the reviewed extension) cannot be borrowed by another kind.
func validateResourceSourceFile(model core.MediaModel, subtype string, file referencevalidation.File) error {
	if model.ResourceOperation == nil {
		return fmt.Errorf("%w: no resource source schema", core.ErrOptionsInvalid)
	}
	scoped := model
	scoped.ReferenceMediaLimits = nil
	for _, variant := range model.ResourceOperation.Variants {
		if variant.Subtype != subtype {
			continue
		}
		if len(variant.SourceKinds) > 0 && !slices.Contains(variant.SourceKinds, file.Type) {
			return fmt.Errorf("%w: unsupported resource source kind", core.ErrOptionsInvalid)
		}
		if len(variant.AllowedDimensions) > 0 && !slices.Contains(variant.AllowedDimensions, core.MediaDimensions{Width: file.Info.Width, Height: file.Info.Height}) {
			return fmt.Errorf("%w: unsupported resource source dimensions", core.ErrOptionsInvalid)
		}
		scoped.ReferenceMediaLimits = variant.Limits
		break
	}
	if err := referencevalidation.ValidateFiles(scoped, []referencevalidation.File{file}, false); err != nil {
		return fmt.Errorf("%w: %v", core.ErrOptionsInvalid, err)
	}
	return nil
}
