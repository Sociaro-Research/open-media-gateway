package core

import (
	"fmt"
	"github.com/google/uuid"
	"slices"
	"strings"
	"unicode/utf8"
)

func CloneResourceIntent(in *ResourceIntent) *ResourceIntent {
	if in == nil {
		return nil
	}
	out := *in
	out.ReferenceIDs = slices.Clone(in.ReferenceIDs)
	out.Tags = slices.Clone(in.Tags)
	return &out
}
func CloneResourceCreation(in *ResourceCreateRequest) *ResourceCreateRequest {
	if in == nil {
		return nil
	}
	out := *in
	clone := func(sources []ResourceSourceInput) []ResourceSourceInput {
		out := slices.Clone(sources)
		for i := range out {
			out[i].AudioCodecs = CloneAudioCodecs(out[i].AudioCodecs)
			if out[i].AudioStreams != nil {
				v := *out[i].AudioStreams
				out[i].AudioStreams = &v
			}
			if out[i].VideoStreams != nil {
				v := *out[i].VideoStreams
				out[i].VideoStreams = &v
			}
			if d := out[i].Duration; d != nil {
				v := *d
				out[i].Duration = &v
			}
		}
		return out
	}
	out.ReferenceSources = slices.Clone(in.ReferenceSources)
	out.FileSources = clone(in.FileSources)
	out.Images = clone(in.Images)
	out.Videos = clone(in.Videos)
	out.VoiceSources = clone(in.VoiceSources)
	out.Tags = slices.Clone(in.Tags)
	out.GeneratedVideo = CloneGeneratedResourceSource(in.GeneratedVideo)
	out.GeneratedMedia = CloneGeneratedResourceSource(in.GeneratedMedia)
	if in.BoundVoice != nil {
		v := *in.BoundVoice
		v.Session = CloneMediaSession(v.Session)
		v.Sources = CloneResourceSources(v.Sources)
		out.BoundVoice = &v
	}
	return &out
}
func ValidateResourceCreationIntent(model MediaModel, o MediaOptions, deferResolution bool) error {
	invalid := func() error {
		return resourceIntentError("", "could not be resolved for this model; review the preparation inputs and estimate again")
	}
	if model.ResourceOperation == nil {
		if o.ResourceIntent != nil || o.ResourceCreation != nil {
			return invalid()
		}
		return nil
	}
	intent := o.ResourceIntent
	schema := model.ResourceOperation
	if intent == nil {
		return resourceIntentError("", "is required; supply the preparation inputs")
	}
	if intent.Kind != schema.Kind {
		return resourceIntentError("kind", "does not match this preparation; choose a supported preparation kind")
	}
	if strings.TrimSpace(intent.Name) == "" {
		return resourceIntentError("name", "must be nonempty; supply a resource name")
	}
	if len(intent.Name) > 256 {
		return resourceIntentError("name", fmt.Sprintf("has %d bytes; maximum 256 bytes. Shorten the name", len(intent.Name)))
	}
	if len(intent.Description) > 4096 {
		return resourceIntentError("description", fmt.Sprintf("has %d bytes; maximum 4096 bytes. Shorten the description", len(intent.Description)))
	}
	if len(intent.ReferenceIDs) > 64 {
		return resourceIntentError("reference_ids", "must contain at most 64 sources; remove extra sources")
	}
	if len(intent.Tags) > 32 {
		return resourceIntentError("tags", "must contain at most 32 tags; remove extra tags")
	}
	if len(schema.Subtypes) > 0 && !slices.Contains(schema.Subtypes, intent.Subtype) {
		hint := "is unsupported; choose a documented preparation subtype"
		if choices, ok := parameterChoices(schema.Subtypes); ok {
			hint = "must be one of: " + choices + ". Choose a supported subtype"
		}
		return resourceIntentError("subtype", hint)
	}
	if schema.NameMaxCharacters > 0 && utf8.RuneCountInString(intent.Name) > schema.NameMaxCharacters {
		return resourceIntentError("name", fmt.Sprintf("has %d characters; maximum %d Unicode code points. Shorten the name", utf8.RuneCountInString(intent.Name), schema.NameMaxCharacters))
	}
	if schema.NameMaxCharacters > 0 && !schema.SupportsDescription && intent.Description != "" {
		return resourceIntentError("description", "is not supported by this preparation; omit it")
	}
	if schema.DescriptionRequired && strings.TrimSpace(intent.Description) == "" {
		return resourceIntentError("description", "is required; supply a nonempty description")
	}
	if schema.DescriptionMaxCharacters > 0 && utf8.RuneCountInString(intent.Description) > schema.DescriptionMaxCharacters {
		return resourceIntentError("description", fmt.Sprintf("has %d characters; maximum %d Unicode code points. Shorten the description", utf8.RuneCountInString(intent.Description), schema.DescriptionMaxCharacters))
	}
	if schema.MaxTags > 0 && len(intent.Tags) > schema.MaxTags {
		return resourceIntentError("tags", fmt.Sprintf("must contain at most %d tags; remove extra tags", schema.MaxTags))
	}
	seen := map[string]bool{}
	for i, id := range intent.ReferenceIDs {
		if _, err := uuid.Parse(id); err != nil {
			return resourceIntentError(fmt.Sprintf("reference_ids.%d", i), "must be a UUID; select an uploaded reference")
		}
		if seen[id] {
			return resourceIntentError(fmt.Sprintf("reference_ids.%d", i), "is a duplicate source; remove the duplicate reference")
		}
		seen[id] = true
	}
	for i, id := range []string{intent.GeneratedTaskID, intent.BoundVoiceBindingID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				field := []string{"generated_task_id", "bound_voice_binding_id"}[i]
				return resourceIntentError(field, "must be a UUID; select an available source")
			}
		}
	}
	if intent.GeneratedArtifactID != "" && (intent.GeneratedTaskID == "" || !ValidGeneratedArtifactID(intent.GeneratedArtifactID)) {
		return resourceIntentError("generated_artifact_id", "requires generated_task_id and a valid artifact identifier; select an output of the source generation")
	}
	for i, tag := range intent.Tags {
		if len(tag) > 128 {
			return resourceIntentError(fmt.Sprintf("tags.%d", i), "must contain at most 128 bytes; shorten this tag")
		}
	}
	if len(intent.ReferenceIDs) == 0 && intent.GeneratedTaskID == "" {
		return resourceIntentError("reference_ids", "requires at least one source; select an uploaded reference or supply generated_task_id")
	}
	if deferResolution {
		return nil
	}
	request := o.ResourceCreation
	if request == nil || request.Kind != intent.Kind || request.Subtype != intent.Subtype || request.Name != intent.Name || request.Description != intent.Description || !slices.Equal(request.Tags, intent.Tags) {
		return invalid()
	}
	actual := map[string]bool{}
	for _, group := range [][]ResourceSourceInput{request.Images, request.Videos, request.VoiceSources, request.FileSources} {
		for _, source := range group {
			if source.ReferenceID == "" || actual[source.ReferenceID] || !seen[source.ReferenceID] {
				return invalid()
			}
			actual[source.ReferenceID] = true
		}
	}
	if len(actual) != len(seen) || (request.GeneratedVideo != nil || request.GeneratedMedia != nil) != (intent.GeneratedTaskID != "") || request.GeneratedVideo != nil && request.GeneratedMedia != nil || (request.BoundVoice != nil) != (intent.BoundVoiceBindingID != "") {
		return invalid()
	}
	if request.GeneratedMedia != nil {
		if request.GeneratedMedia.TaskID != intent.GeneratedTaskID || intent.GeneratedArtifactID != "" && request.GeneratedMedia.ArtifactID != intent.GeneratedArtifactID {
			return invalid()
		}
		if err := ValidateGeneratedMediaSource(model, intent.Subtype, request.GeneratedMedia); err != nil {
			return err
		}
	}
	if request.GeneratedVideo != nil && (request.GeneratedVideo.TaskID != intent.GeneratedTaskID || intent.GeneratedArtifactID != "") {
		return invalid()
	}
	if request.BoundVoice != nil && request.BoundVoice.BindingID != intent.BoundVoiceBindingID {
		return invalid()
	}
	return nil
}
