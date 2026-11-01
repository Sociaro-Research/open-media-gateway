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
	out.FileSources = clone(in.FileSources)
	out.Images = clone(in.Images)
	out.Videos = clone(in.Videos)
	out.VoiceSources = clone(in.VoiceSources)
	out.Tags = slices.Clone(in.Tags)
	if in.GeneratedVideo != nil {
		v := *in.GeneratedVideo
		if v.Duration != nil {
			d := *v.Duration
			v.Duration = &d
		}
		out.GeneratedVideo = &v
	}
	if in.BoundVoice != nil {
		v := *in.BoundVoice
		v.Session = CloneMediaSession(v.Session)
		v.Sources = slices.Clone(v.Sources)
		for i := range v.Sources {
			if v.Sources[i].Duration != nil {
				d := *v.Sources[i].Duration
				v.Sources[i].Duration = &d
			}
		}
		out.BoundVoice = &v
	}
	return &out
}
func ValidateResourceCreationIntent(model MediaModel, o MediaOptions, deferResolution bool) error {
	invalid := func() error {
		return fmt.Errorf("%w: invalid or unresolved resource creation intent", ErrOptionsInvalid)
	}
	if model.ResourceOperation == nil {
		if o.ResourceIntent != nil || o.ResourceCreation != nil {
			return invalid()
		}
		return nil
	}
	intent := o.ResourceIntent
	if intent == nil || intent.Kind != model.ResourceOperation.Kind || strings.TrimSpace(intent.Name) == "" || len(intent.Name) > 256 || len(intent.Description) > 4096 || len(intent.ReferenceIDs) > 64 || len(intent.Tags) > 32 || len(model.ResourceOperation.Subtypes) > 0 && !slices.Contains(model.ResourceOperation.Subtypes, intent.Subtype) {
		return invalid()
	}
	schema := model.ResourceOperation
	if schema.NameMaxCharacters > 0 && utf8.RuneCountInString(intent.Name) > schema.NameMaxCharacters {
		return invalid()
	}
	if schema.NameMaxCharacters > 0 && !schema.SupportsDescription && intent.Description != "" {
		return invalid()
	}
	if schema.DescriptionRequired && strings.TrimSpace(intent.Description) == "" {
		return invalid()
	}
	if schema.DescriptionMaxCharacters > 0 && utf8.RuneCountInString(intent.Description) > schema.DescriptionMaxCharacters {
		return invalid()
	}
	if schema.MaxTags > 0 && len(intent.Tags) > schema.MaxTags {
		return invalid()
	}
	seen := map[string]bool{}
	for _, id := range intent.ReferenceIDs {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return invalid()
		}
		seen[id] = true
	}
	for _, id := range []string{intent.GeneratedTaskID, intent.BoundVoiceBindingID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return invalid()
			}
		}
	}
	for _, tag := range intent.Tags {
		if len(tag) > 128 {
			return invalid()
		}
	}
	if len(intent.ReferenceIDs) == 0 && intent.GeneratedTaskID == "" {
		return invalid()
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
	if len(actual) != len(seen) || (request.GeneratedVideo != nil) != (intent.GeneratedTaskID != "") || (request.BoundVoice != nil) != (intent.BoundVoiceBindingID != "") {
		return invalid()
	}
	if request.GeneratedVideo != nil && request.GeneratedVideo.TaskID != intent.GeneratedTaskID {
		return invalid()
	}
	if request.BoundVoice != nil && request.BoundVoice.BindingID != intent.BoundVoiceBindingID {
		return invalid()
	}
	return nil
}
