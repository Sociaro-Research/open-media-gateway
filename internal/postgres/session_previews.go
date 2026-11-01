package postgres

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

// A session may reference only a validated image archived by this exact terminal
// transition. Adapter-supplied selectors cannot link another task's content.
func validateSessionPreviewArtifacts(resources []core.ProviderResource, artifacts []core.StoredArtifact) error {
	for _, resource := range resources {
		if resource.Session == nil {
			continue
		}
		if err := providerresources.ValidateSession(resource.Kind, resource.Session); err != nil {
			return err
		}
		for _, face := range resource.Session.Faces {
			if face.PreviewArtifactID == "" {
				continue
			}
			matches := 0
			for _, a := range artifacts {
				if a.ID == face.PreviewArtifactID {
					if a.MediaType != "image" || a.StorageKey == "" || a.SizeBytes <= 0 || a.SHA256 == "" {
						return providerresources.ErrInvalid
					}
					matches++
				}
			}
			if matches != 1 {
				return providerresources.ErrInvalid
			}
		}
	}
	return nil
}
