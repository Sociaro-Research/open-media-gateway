package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var generatedArtifactID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// GeneratedTaskMediaType maps generation categories to their archived file kind.
// The exact reviewed source model is still checked independently.
func GeneratedTaskMediaType(kind string) string {
	switch kind {
	case "speech", "music":
		return "audio"
	default:
		return kind
	}
}

func ValidGeneratedArtifactID(id string) bool { return generatedArtifactID.MatchString(id) }

func CloneGeneratedResourceSource(in *GeneratedResourceSource) *GeneratedResourceSource {
	if in == nil {
		return nil
	}
	out := *in
	if in.Duration != nil {
		d := *in.Duration
		out.Duration = &d
	}
	if in.Inspection != nil {
		e := CloneInputMediaEvidence(map[string]InputMediaEvidence{"source": *in.Inspection})["source"]
		out.Inspection = &e
	}
	return &out
}

// ValidateGeneratedMediaSource checks typed host evidence. Authorization and
// fresh byte inspection are performed by the host before this private value is
// accepted; timestamps remain immutable provenance, not caller duration claims.
func ValidateGeneratedMediaSource(model MediaModel, subtype string, source *GeneratedResourceSource) error {
	invalid := func() error { return fmt.Errorf("%w: invalid generated media provenance", ErrOptionsInvalid) }
	if source == nil || model.ResourceOperation == nil || source.TaskID == "" || !ValidGeneratedArtifactID(source.ArtifactID) || source.OutputID == "" || source.RemoteID != "" || source.Model == "" || source.Operation != source.Model || source.MediaType == "" || source.CreatedAt.IsZero() || !source.ExpiresAt.After(source.CreatedAt) {
		return invalid()
	}
	hash, err := hex.DecodeString(source.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return invalid()
	}
	for _, variant := range model.ResourceOperation.Variants {
		if variant.Subtype != subtype {
			continue
		}
		for _, rule := range variant.GeneratedSources {
			kind := rule.MediaType
			if kind == "" {
				kind = "video"
			}
			if rule.Model != source.Model || kind != source.MediaType {
				continue
			}
			if rule.NativeAudioRequired && !source.NativeAudio {
				return invalid()
			}
			if rule.MaxAgeSeconds > 0 && source.ExpiresAt.Sub(source.CreatedAt).Seconds() > float64(rule.MaxAgeSeconds) {
				return invalid()
			}
			if source.Duration == nil || *source.Duration <= 0 || math.IsNaN(*source.Duration) || math.IsInf(*source.Duration, 0) || rule.MinDuration > 0 && *source.Duration < rule.MinDuration || rule.MaxDuration > 0 && *source.Duration > rule.MaxDuration {
				return invalid()
			}
			if rule.RequiresInspectedOutput {
				e := source.Inspection
				if e == nil || e.ReferenceID != source.TaskID || e.Type != source.MediaType || e.SHA256 != source.SHA256 || e.SizeBytes <= 0 || !strings.HasPrefix(e.ContentType, source.MediaType+"/") || e.Duration != *source.Duration || e.Duration <= 0 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) {
					return invalid()
				}
			}
			return nil
		}
	}
	return invalid()
}

// GeneratedResourceEvidence is a private durable source snapshot. The local task
// ID retains the existing reference identity contract; artifact identity is
// separately pinned so a different output of the same task cannot be substituted.
func GeneratedResourceEvidence(source *GeneratedResourceSource) ResourceSourceEvidence {
	if source == nil {
		return ResourceSourceEvidence{}
	}
	out := ResourceSourceEvidence{ReferenceID: source.TaskID, MediaType: source.MediaType, SHA256: source.SHA256, Duration: source.Duration, Inspection: source.Inspection, Provenance: &GeneratedSourceProvenance{TaskID: source.TaskID, ArtifactID: source.ArtifactID, Model: source.Model, CreatedAt: source.CreatedAt, ExpiresAt: source.ExpiresAt}}
	return CloneResourceSources([]ResourceSourceEvidence{out})[0]
}

// ResourceSourcesExpired checks reusable generated-source lifetime without
// consulting mutable local archive availability after a binding is accepted.
func ResourceSourcesExpired(sources []ResourceSourceEvidence, now time.Time) bool {
	for _, source := range sources {
		if p := source.Provenance; p != nil {
			if p.CreatedAt.IsZero() || now.Before(p.CreatedAt) || !now.Before(p.ExpiresAt) {
				return true
			}
		}
	}
	return false
}
