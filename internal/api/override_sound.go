package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
)

func (s *Server) resolveOverrideSound(r *http.Request, input *core.CreateMediaInput) error {
	// Never trust persisted-source fields supplied over HTTP, including template options.
	input.Options.OverrideSound = nil
	id := input.Options.OverrideSoundID
	if id == "" {
		return nil
	}
	invalid := func() error {
		return fmt.Errorf("%w: Override Sound requires an accessible audio library file and video output", references.ErrInvalidImage)
	}
	if input.MediaType != "video" || s.references == nil {
		return invalid()
	}
	ref, ctx, err := s.generationReference(r.Context(), id)
	if err != nil || !strings.HasPrefix(ref.ContentType, "audio/") || ref.SHA256 == "" {
		return invalid()
	}
	if _, err := s.inspectTemplateMedia(references.StorageContext(ctx, ref), ref.StorageKey, ref.ContentType, ref.SizeBytes, ref.SHA256); err != nil {
		return invalid()
	}
	input.Options.OverrideSound = &core.SoundOverride{ReferenceID: ref.ID, OwnerID: identity.OwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx), DataOwnerID: identity.OwnerID(references.StorageContext(ctx, ref)), StorageKey: ref.StorageKey, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}
	return nil
}
