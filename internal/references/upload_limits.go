package references

import (
	"context"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

// HardMaxBytes bounds inspection and transport of an authorized extended video.
// It does not increase ordinary upload or model admission limits.
const HardMaxBytes int64 = referencevalidation.HardVideoMaxBytes

type UploadLimitResolver func(context.Context, *referencevalidation.Target) (int64, error)
type uploadLimitResolverKey struct{}

// WithUploadLimitResolver installs host authorization, never client metadata.
func WithUploadLimitResolver(ctx context.Context, resolver UploadLimitResolver) context.Context {
	return context.WithValue(ctx, uploadLimitResolverKey{}, resolver)
}

// UploadLimit returns an inclusive per-upload ceiling. A target alone cannot
// raise it: the host must resolve the target against the authorized catalog.
func UploadLimit(ctx context.Context, target *referencevalidation.Target) (int64, error) {
	if target == nil {
		return MaxBytes, nil
	}
	resolver, _ := ctx.Value(uploadLimitResolverKey{}).(UploadLimitResolver)
	if resolver == nil {
		return MaxBytes, nil
	}
	limit, err := resolver(ctx, target)
	if err != nil {
		return 0, err
	}
	if limit <= 0 || limit > HardMaxBytes {
		return 0, referencevalidation.Invalid("References", "The authorized upload limit is invalid. Select the model again.")
	}
	return limit, nil
}

func checkUploadLimit(ctx context.Context, target *referencevalidation.Target, name, kind string, size int64) error {
	limit, err := UploadLimit(ctx, target)
	if err != nil {
		return err
	}
	if formatLimit := commonLimit(kind); formatLimit == 0 {
		return checkUpload(UploadInput{Name: name, ContentType: kind, SizeBytes: size})
	} else if kind != "video/mp4" && kind != "video/quicktime" && kind != "video/webm" && formatLimit < limit {
		limit = formatLimit
	}
	if size <= 0 || size > limit {
		return referencevalidation.Invalid(cleanName(name), "The file exceeds the authorized upload size limit. Reduce the file size or select a supported model.")
	}
	return nil
}

// Resource-source read permission is private host context, pinned to the exact
// accepted operation, owner and actual connection. It is never an upload DTO.
type resourceSourceModelKey struct{}
type resourceSourceModelProof struct {
	owner, operation string
	scope            core.ProviderConnectionScope
	limit            int64
}

func WithResourceSourceModel(ctx context.Context, operation string, model core.MediaModel) context.Context {
	proof := resourceSourceModelProof{}
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if ok && scope.ID != "" && scope.OwnerID != "" && scope.Revision != "" && identity.OwnerID(ctx) != "" && operation != "" && operation == model.ID && !model.Manual && model.SupportsVideoInput && model.ReferenceMediaLimits != nil {
		proof = resourceSourceModelProof{owner: identity.OwnerID(ctx), operation: operation, scope: scope, limit: referencevalidation.ReviewedVideoMaxBytes(model)}
	}
	return context.WithValue(ctx, resourceSourceModelKey{}, proof)
}

func resourceSourceLimit(ctx context.Context, source core.ResourceSourceInput) int64 {
	limit := MaxBytes
	proof, ok := ctx.Value(resourceSourceModelKey{}).(resourceSourceModelProof)
	scope, scoped := core.ProviderConnectionScopeFromContext(ctx)
	if ok && scoped && proof.operation != "" && proof.owner == identity.OwnerID(ctx) && proof.scope == scope && proof.limit > limit && proof.limit <= HardMaxBytes && source.MediaType == "video" {
		if revision := core.ProviderRevision(ctx); revision == "" || revision == proof.scope.Revision {
			limit = proof.limit
		}
	}
	return limit
}
