package core

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"

	"github.com/google/uuid"
)

type ResourceInputSchema struct {
	SupportsPresets bool     `json:"supports_presets,omitempty"`
	Kind            string   `json:"kind"`
	MaxCount        int      `json:"max_count"`
	Subtypes        []string `json:"subtypes,omitempty"`
}

// ReferenceResourceResolver resolves only locally authorized resource bindings.
// The host supplies the concrete provider connection; no remote ownership or
// duration assertion is accepted from HTTP callers.
type ReferenceResourceResolver interface {
	ResolveResourceReferences(context.Context, string, []ResourceReference) ([]ResourceSnapshot, error)
}

func (s *TaskService) SetReferenceResourceResolver(r ReferenceResourceResolver) {
	s.resourceResolver = r
}

var resourceAlias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidateResourceReferenceIntent checks immutable client syntax before mutable lookup.
func ValidateResourceReferenceIntent(model MediaModel, options MediaOptions) error {
	if len(options.ResourceReferences) > 32 || (len(options.ResourceReferences) > 0 && len(model.ResourceInputSchemas) == 0) {
		return fmt.Errorf("%w: unsupported resource references", ErrOptionsInvalid)
	}
	names := map[string]bool{}
	for kind, count := range map[string]int{"image": len(options.ImageURLs), "video": len(options.Videos()), "audio": len(options.Audios())} {
		for i := 0; i < count; i++ {
			names[fmt.Sprintf("%s_%d", kind, i+1)] = true
		}
	}
	bindings := map[string]bool{}
	for _, ref := range options.ResourceReferences {
		if _, err := uuid.Parse(ref.BindingID); err != nil || !resourceAlias.MatchString(ref.ReferenceID) || names[ref.ReferenceID] || bindings[ref.BindingID] {
			return fmt.Errorf("%w: invalid resource binding or alias", ErrOptionsInvalid)
		}
		names[ref.ReferenceID] = true
		bindings[ref.BindingID] = true
	}
	return nil
}
func hasVoiceResourceIntent(model MediaModel, o MediaOptions) bool {
	for _, snapshot := range o.ResourceSnapshots {
		if snapshot.Kind == "voice" {
			return true
		}
	}
	// A voice-only model gives the unresolved intent an unambiguous kind. This
	// keeps defaults identical when an accepted idempotent retry outlives a resource.
	return len(o.ResourceReferences) > 0 && len(model.ResourceInputSchemas) == 1 && model.ResourceInputSchemas[0].Kind == "voice"
}

func ValidateResourceReferences(model MediaModel, options MediaOptions) error {
	if err := ValidateResourceReferenceIntent(model, options); err != nil {
		return err
	}
	if len(options.ResourceReferences) > 32 || len(options.ResourceReferences) != len(options.ResourceSnapshots) {
		return fmt.Errorf("%w: unresolved resource references", ErrOptionsInvalid)
	}
	names := map[string]bool{}
	for kind, count := range map[string]int{"image": len(options.ImageURLs), "video": len(options.Videos()), "audio": len(options.Audios())} {
		for i := 0; i < count; i++ {
			names[fmt.Sprintf("%s_%d", kind, i+1)] = true
		}
	}
	counts := map[string]int{}
	for i, ref := range options.ResourceReferences {
		snapshot := options.ResourceSnapshots[i]
		if _, err := uuid.Parse(ref.BindingID); err != nil {
			return fmt.Errorf("%w: invalid resource binding", ErrOptionsInvalid)
		}
		if !resourceAlias.MatchString(ref.ReferenceID) || names[ref.ReferenceID] || snapshot.BindingID != ref.BindingID || snapshot.ReferenceID != ref.ReferenceID || snapshot.RemoteID == "" || snapshot.ConnectionIdentity == "" {
			return fmt.Errorf("%w: invalid resource snapshot", ErrOptionsInvalid)
		}
		names[ref.ReferenceID] = true
		counts[snapshot.Kind]++
		allowed := false
		for _, schema := range model.ResourceInputSchemas {
			if schema.Kind == snapshot.Kind && (!snapshot.IsPreset || schema.SupportsPresets) && counts[snapshot.Kind] <= schema.MaxCount && (len(schema.Subtypes) == 0 || slices.Contains(schema.Subtypes, snapshot.Subtype)) {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("%w: unsupported resource kind, subtype or count", ErrOptionsInvalid)
		}
	}
	if model.MaxReferenceInputs > 0 && len(options.ImageURLs)+len(options.Videos())+len(options.Audios())+len(options.ResourceReferences) > model.MaxReferenceInputs {
		return fmt.Errorf("%w: combined reference count exceeded", ErrOptionsInvalid)
	}
	return nil
}

// ResourceInputVideoDuration is separate from URL video duration because each
// model's billing contract decides whether video-character resources are charged.
func (o MediaOptions) ResourceInputVideoDuration() (float64, bool) {
	total := 0.0
	for _, r := range o.ResourceSnapshots {
		if r.Kind != "element" || r.Subtype != "video_character" {
			continue
		}
		found := false
		for _, s := range r.Sources {
			if s.MediaType == "video" {
				if s.Duration == nil || *s.Duration <= 0 || math.IsNaN(*s.Duration) || math.IsInf(*s.Duration, 0) {
					return 0, false
				}
				total += *s.Duration
				found = true
			}
		}
		if !found {
			return 0, false
		}
	}
	return total, true
}

// ResourceReference identifies an owner-scoped Gateway binding and its prompt
// alias. It never accepts a remote provider ID or caller-asserted media metadata.
type ResourceReference struct {
	BindingID   string `json:"binding_id"`
	ReferenceID string `json:"reference_id"`
}

// ResourceSourceEvidence pins inspected source identity and duration with a job.
// Unknown duration remains nil; it cannot be interpreted as a zero-length video.
type ResourceSourceEvidence struct {
	Inspection  *InputMediaEvidence `json:"inspection,omitempty"`
	ReferenceID string              `json:"reference_id"`
	MediaType   string              `json:"media_type"`
	SHA256      string              `json:"sha256"`
	Duration    *float64            `json:"duration,omitempty"`
}

// ResourceSnapshot is server-resolved evidence, persisted privately with an
// accepted request. RemoteID remains a string even when vendor examples use a
// numeric JSON token, preserving IDs larger than JavaScript's safe integer range.
type ResourceSnapshot struct {
	Session            *MediaSessionEvidence    `json:"session,omitempty"`
	BindingID          string                   `json:"binding_id"`
	ReferenceID        string                   `json:"reference_id"`
	Kind               string                   `json:"kind"`
	RemoteID           string                   `json:"remote_id"`
	IsPreset           bool                     `json:"is_preset,omitempty"`
	Subtype            string                   `json:"subtype,omitempty"`
	ConnectionIdentity string                   `json:"connection_identity"`
	Sources            []ResourceSourceEvidence `json:"sources,omitempty"`
}
