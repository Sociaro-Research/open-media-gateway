package core

import (
	"fmt"
	"slices"
)

// MediaInputCounts counts URL inputs and reviewed managed files by media kind.
// Elements, voices and sessions retain their own resource semantics.
type MediaInputCounts struct {
	Images int
	Videos int
	Audios int
}

func (c MediaInputCounts) Count(kind string) int {
	switch kind {
	case "image":
		return c.Images
	case "video":
		return c.Videos
	case "audio":
		return c.Audios
	}
	return 0
}

// CountMediaInputs accepts only paired, host-resolved resource snapshots. A file
// binding is one input regardless of how many provenance sources it retains.
// Files follow URL inputs of the same kind, matching their positional aliases.
// An adapter that materializes files into URLs must clear the consumed resource
// pairs; the resulting URLs then count once through the ordinary path.
func CountMediaInputs(model MediaModel, options MediaOptions) (MediaInputCounts, error) {
	counts := MediaInputCounts{Images: len(options.ImageURLs), Videos: len(options.Videos()), Audios: len(options.Audios())}
	if err := ValidateResourceReferences(model, options); err != nil {
		return MediaInputCounts{}, err
	}
	for _, resource := range options.ResourceSnapshots {
		if resource.Kind != "file_resource" {
			continue
		}
		reviewed := false
		for _, schema := range model.ResourceInputSchemas {
			if schema.Kind == "file_resource" && slices.Contains(schema.Subtypes, resource.Subtype) {
				reviewed = true
				break
			}
		}
		if !reviewed {
			return MediaInputCounts{}, fmt.Errorf("%w: file media kind lacks an explicit reviewed schema", ErrOptionsInvalid)
		}
		if resource.Subtype != "image" && resource.Subtype != "video" && resource.Subtype != "audio" {
			// A model may support other file-resource kinds, such as owned regeneration
			// sources. They are resources rather than media-array inputs.
			continue
		}
		for _, source := range resource.Sources {
			if source.MediaType != "" && source.MediaType != resource.Subtype {
				return MediaInputCounts{}, fmt.Errorf("%w: file media kind conflicts with inspected provenance", ErrOptionsInvalid)
			}
		}
		index := counts.Count(resource.Subtype) + 1
		if resource.ReferenceID != fmt.Sprintf("%s_%d", resource.Subtype, index) {
			return MediaInputCounts{}, fmt.Errorf("%w: file references must preserve ordered media positions", ErrOptionsInvalid)
		}
		switch resource.Subtype {
		case "image":
			counts.Images++
		case "video":
			counts.Videos++
		case "audio":
			counts.Audios++
		}
	}
	return counts, nil
}
